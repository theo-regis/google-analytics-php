package beacon

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html/template"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"

	"appengine"
	"appengine/delay"
	"appengine/urlfetch"

	"encoding/json"
	"os"
)

const beaconURL = "http://www.google-analytics.com/collect"

var (
	pixel        = mustReadFile("static/pixel.gif")
	badge        = mustReadFile("static/badge.svg")
	badgeGif     = mustReadFile("static/badge.gif")
	badgeFlat    = mustReadFile("static/badge-flat.svg")
	badgeFlatGif = mustReadFile("static/badge-flat.gif")
	pageTemplate = template.Must(template.New("page").ParseFiles("ga-beacon/page.html"))
)

func init() {
	http.HandleFunc("/", handler)
}

func mustReadFile(path string) []byte {
	b, err := ioutil.ReadFile(path)
	if err != nil {
		panic(err)
	}
	return b
}

func generateUUID(cid *string) error {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		return err
	}

	b[8] = (b[8] | 0x80) & 0xBF // what's the purpose ?
	b[6] = (b[6] | 0x40) & 0x4F // what's the purpose ?
	*cid = hex.EncodeToString(b)
	return nil
}

var delayHit = delay.Func("collect", logHit)

func log(c appengine.Context, ua string, ip string, cid string, values url.Values) error {
	req, _ := http.NewRequest("POST", beaconURL, strings.NewReader(values.Encode()))
	req.Header.Add("User-Agent", ua)
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")

	if resp, err := urlfetch.Client(c).Do(req); err != nil {
		c.Errorf("GA collector POST error: %s", err.Error())
		return err
	} else {
		c.Debugf("GA collector status: %v, cid: %v, ip: %s", resp.Status, cid, ip)
		c.Debugf("Reported payload: %v", values)
	}
	return nil
}

func logHit(c appengine.Context, params []string, query url.Values, ua string, ip string, cid string) error {
	// 1) Initialize default values from path structure
	// 2) Allow query param override to report arbitrary values to GA
	//
	// GA Protocol reference: https://developers.google.com/analytics/devguides/collection/protocol/v1/reference

	payload := url.Values{
		"v":   {"1"},        // protocol version = 1
		"t":   {"pageview"}, // hit type
		"tid": {params[0]},  // tracking / property ID
		"cid": {cid},        // unique client ID (server generated UUID)
		"dp":  {params[1]},  // page path
		"uip": {ip},         // IP address of the user
	}

	for key, val := range query {
		payload[key] = val
	}

	return log(c, ua, ip, cid, payload)
}

func handler(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)
	params := strings.SplitN(strings.Trim(r.URL.Path, "/"), "/", 2)
	query, _ := url.ParseQuery(r.URL.RawQuery)
	refOrg := r.Header.Get("Referer")

	// / -> redirect
	if len(params[0]) == 0 {
		http.Redirect(w, r, "http://imailing.com.br/", http.StatusFound)
		return
	}

	// check allowed Tracking Ids
	config := readConfig("ga-beacon/conf.json")
	if !checkTrackingId(params[0], config.TrackingIds) {
		http.Error(w, "Forbidden, read http://imailing.com.br/", 403)
		return
	}

	// activate referrer path if ?useReferer is used and if referer exists
	if _, ok := query["useReferer"]; ok {
		if len(refOrg) != 0 {
			referer := strings.Replace(strings.Replace(refOrg, "http://", "", 1), "https://", "", 1);
			if len(referer) != 0 {
				// if the useReferer is present and the referer information exists
				//  the path is ignored and the beacon referer information is used instead.
				params = strings.SplitN(strings.Trim(r.URL.Path, "/") + "/" + referer, "/", 2)
			}
		}
	}
	// /account -> account template
	if len(params) == 1 {
		templateParams := struct {
			Account string
			Referer string
		}{
			Account: params[0],
			Referer: refOrg,
		}
		if err := pageTemplate.ExecuteTemplate(w, "page.html", templateParams); err != nil {
			http.Error(w, "could not show account page", 500)
			c.Errorf("Cannot execute template: %v", err)
		}
		return
	}

	// /account/page -> GIF + log pageview to GA collector
	var cid string
	if cookie, err := r.Cookie("cid"); err != nil {
		if err := generateUUID(&cid); err != nil {
			c.Debugf("Failed to generate client UUID: %v", err)
		} else {
			c.Debugf("Generated new client UUID: %v", cid)
			http.SetCookie(w, &http.Cookie{Name: "cid", Value: cid, Path: fmt.Sprint("/", params[0])})
		}
	} else {
		cid = cookie.Value
		c.Debugf("Existing CID found: %v", cid)
	}

	if len(cid) != 0 {
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("CID", cid)

		logHit(c, params, query, r.Header.Get("User-Agent"), r.RemoteAddr, cid)
		// delayHit.Call(c, params, r.Header.Get("User-Agent"), cid)
	}

	// Write out GIF pixel or badge, based on presence of "pixel" param.
	//w.Header().Set("Content-Type", "image/gif")
	//w.Write(pixel)
	http.Error(w, "Forbidden, query.Get("link"), 403)
	http.Redirect(w, r, query.Get("link"), 301)
	
	
}

type Configuration struct {
	TrackingIds []string
}

func readConfig(path string) Configuration {
	file, _ := os.Open(path)
	decoder := json.NewDecoder(file)
	configuration := Configuration{}
	err := decoder.Decode(&configuration)
	if err != nil {
		fmt.Println("error:", err)
	}
	return configuration
}

func checkTrackingId(trackingId string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, item := range allowed {
		if item == trackingId {
			return true
		}
	}
	return false
}
