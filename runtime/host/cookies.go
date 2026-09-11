package host

import (
	"bufio"
	"bytes"
	"net/http"
	"strconv"
	"strings"
	"time"
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
// IncludeSubdomains=FALSE produces a host-only cookie (cookiejar semantics:
// Domain set to the bare host), TRUE produces a domain cookie (Domain prefixed
// with "."), and Secure is carried over.
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
