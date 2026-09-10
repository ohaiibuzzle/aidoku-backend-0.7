package host

import (
	"encoding/json"
	"regexp"
)

// contentRuleList parses and matches the WKContentRuleList JSON DSL
// AidokuRunner sources pass to js.webview_set_rule_list, e.g.:
//
//	[{"trigger":{"url-filter":".*ads.*","resource-type":["image"]},"action":{"type":"block"}}]
//
// Only "block" is meaningful (other WebKit actions have no bearing on a
// non-rendering shim). url-filter is WebKit's ICU-regex dialect, compiled
// with Go's RE2-based regexp -- covers common patterns but not
// backreferences/lookaround; an uncompilable rule is skipped, not fatal.
type contentRuleList struct {
	rules []contentRule
}

type contentRule struct {
	urlFilter     *regexp.Regexp
	resourceTypes map[string]bool // empty set means "matches every resource type"
}

type ruleListJSON []struct {
	Trigger struct {
		URLFilter    string   `json:"url-filter"`
		ResourceType []string `json:"resource-type"`
	} `json:"trigger"`
	Action struct {
		Type string `json:"type"`
	} `json:"action"`
}

func parseContentRuleList(data string) (*contentRuleList, error) {
	var raw ruleListJSON
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		return nil, err
	}
	list := &contentRuleList{}
	for _, r := range raw {
		if r.Action.Type != "block" {
			continue
		}
		re, err := regexp.Compile(r.Trigger.URLFilter)
		if err != nil {
			continue
		}
		var types map[string]bool
		if len(r.Trigger.ResourceType) > 0 {
			types = make(map[string]bool, len(r.Trigger.ResourceType))
			for _, t := range r.Trigger.ResourceType {
				types[t] = true
			}
		}
		list.rules = append(list.rules, contentRule{urlFilter: re, resourceTypes: types})
	}
	return list, nil
}

// resourceType values, matching WKContentRuleList's vocabulary for the
// subset this shim actually loads resources for.
const (
	resourceTypeDocument = "document"
	resourceTypeImage    = "image"
	resourceTypeScript   = "script"
	resourceTypeFetch    = "raw" // WebKit calls XHR/fetch bodies "raw"
)

// Blocks reports whether rawURL should be blocked for the given resource
// type. A nil receiver (no rule list set) never blocks.
func (l *contentRuleList) Blocks(rawURL, resourceType string) bool {
	if l == nil {
		return false
	}
	for _, r := range l.rules {
		if len(r.resourceTypes) > 0 && !r.resourceTypes[resourceType] {
			continue
		}
		if r.urlFilter.MatchString(rawURL) {
			return true
		}
	}
	return false
}
