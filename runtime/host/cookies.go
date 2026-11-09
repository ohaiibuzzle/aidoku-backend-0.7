package host

import (
	"bufio"
	"bytes"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ohaiibuzzle/aidokurunner-go/postcard"
)

// ParseNetscapeCookies parses a Netscape "cookies.txt" file (the format
// exported by browser extensions such as "Get cookies.txt LOCALLY" and read
// by curl -b) into *http.Cookie values. The format is one cookie per line of
// tab-separated fields:
//
//	domain \t includeSubdomains(TRUE/FALSE) \t path \t secure(TRUE/FALSE) \t expires(unix) \t name \t value
//
// Comment and blank lines are skipped. Cookies that are already past their
// expiry are dropped; a zero expiry is kept (treated as a non-expiring
// session cookie for the purposes of this jar).
//
// IncludeSubdomains=FALSE produces a host-only cookie (Domain set to the bare
// host -- pass through ForJar before SetCookies), TRUE produces a domain
// cookie (Domain prefixed with "."), and Secure is carried over.
func ParseNetscapeCookies(data []byte) ([]*http.Cookie, error) {
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	now := time.Now()

	var out []*http.Cookie
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		// A standard Netscape cookie export marks an HttpOnly cookie (e.g.
		// cf_clearance) with a literal "#HttpOnly_" prefix before the real
		// tab-separated fields, not a real comment -- curl special-cases it
		// the same way. Any other "#"-prefixed line is a genuine comment.
		if rest, ok := strings.CutPrefix(line, "#HttpOnly_"); ok {
			line = rest
		} else if strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 7 {
			continue
		}
		domain := strings.TrimSpace(fields[0])
		if domain == "" {
			continue
		}
		includeSubdomains := strings.EqualFold(strings.TrimSpace(fields[1]), "TRUE")
		path := fields[2]
		if path == "" {
			path = "/"
		}
		secure := strings.EqualFold(strings.TrimSpace(fields[3]), "TRUE")
		expires, _ := strconv.ParseInt(strings.TrimSpace(fields[4]), 10, 64)
		if expires > 0 && now.After(time.Unix(expires, 0)) {
			continue // expired
		}
		name := strings.TrimSpace(fields[5])
		value := fields[6]

		c := &http.Cookie{
			Name:   name,
			Value:  value,
			Path:   path,
			Secure: secure,
		}
		if includeSubdomains {
			c.Domain = "." + strings.TrimPrefix(domain, ".")
		} else {
			c.Domain = strings.TrimPrefix(domain, ".")
		}
		if expires > 0 {
			c.Expires = time.Unix(expires, 0)
		}
		out = append(out, c)
	}
	return out, sc.Err()
}

// ForJar returns cs ready for cookiejar.SetCookies(u, ...): a host-only cookie
// for u's host (Domain equal to it with no leading ".", in both cookies.txt
// and Chrome/FlareSolverr convention) gets an empty Domain, since per RFC
// 6265 any non-empty Domain makes the jar send it to subdomains too. cs
// itself is left untouched.
func ForJar(u *url.URL, cs []*http.Cookie) []*http.Cookie {
	out := make([]*http.Cookie, len(cs))
	for i, c := range cs {
		if c.Domain != "" && strings.EqualFold(c.Domain, u.Hostname()) {
			cp := *c
			cp.Domain = ""
			c = &cp
		}
		out[i] = c
	}
	return out
}

// The webview cookie imports use the shared process-wide net/http jar, not a
// per-webview WKWebsiteDataStore. Go's cookiejar can't enumerate or delete by
// domain, so they only see cookies the jar would send to the page's URL (none
// before a load), and only as name+value.

// visibleCookies returns the shared jar's cookies for the loaded page.
func (wv *webviewContext) visibleCookies() []*http.Cookie {
	if wv.client.Jar == nil || wv.pageURL == nil {
		return nil
	}
	return wv.client.Jar.Cookies(wv.pageURL)
}

// encodeCookies postcard-encodes visibleCookies as the guest's Vec<Cookie>
// (field order per aidoku-rs: name, value, expires_date, domain, path,
// is_secure, is_http_only). What the jar hides is filled in: Domain is the
// page host, Path "/", flags false.
func (wv *webviewContext) encodeCookies() []byte {
	wr := postcard.NewWriter()
	cookies := wv.visibleCookies()
	wr.WriteLen(len(cookies))
	for _, c := range cookies {
		wr.WriteString(c.Name)
		wr.WriteString(c.Value)
		wr.WriteNone() // expires_date: unknown to the jar
		wr.WriteString(wv.pageURL.Hostname())
		wr.WriteString("/")
		wr.WriteBool(false)
		wr.WriteBool(false)
	}
	return wr.Bytes()
}

// cookieFilter is webview_delete_cookie's selection. An empty name matches
// every cookie; empty value and domain match any.
type cookieFilter struct {
	name, value, domain string
}

func (f cookieFilter) matches(c *http.Cookie) bool {
	return f.name == "" || (c.Name == f.name && (f.value == "" || c.Value == f.value))
}

// deleteCookies expires every visible cookie f selects and returns the names
// that actually left the jar. f.domain must be the page host or a parent of
// it, so other sites' cookies in the shared jar are safe. The jar hides which
// domain owns a cookie and keys each domain separately, so with no domain in f
// the expiry is sent for the host and every parent (the jar ignores public
// suffixes). The path is assumed "/", which the jar hides too.
func (wv *webviewContext) deleteCookies(f cookieFilter) []string {
	if wv.pageURL == nil {
		return nil
	}
	host := wv.pageURL.Hostname()
	if f.domain != "" && host != f.domain && !strings.HasSuffix(host, "."+f.domain) {
		return nil
	}
	domains := []string{f.domain}
	if f.domain == "" {
		domains = []string{host}
		for i := strings.IndexByte(host, '.'); i >= 0; i = strings.IndexByte(host, '.') {
			host = host[i+1:]
			domains = append(domains, host)
		}
	}
	var names []string
	for _, c := range wv.visibleCookies() {
		if !f.matches(c) {
			continue
		}
		exp := make([]*http.Cookie, len(domains))
		for i, d := range domains {
			exp[i] = &http.Cookie{Name: c.Name, Path: "/", MaxAge: -1, Domain: d}
		}
		before := len(wv.visibleCookies())
		wv.client.Jar.SetCookies(wv.pageURL, ForJar(wv.pageURL, exp))
		if len(wv.visibleCookies()) < before {
			names = append(names, c.Name)
		}
	}
	return names
}
