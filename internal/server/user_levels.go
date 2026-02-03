package server

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type UserLevel int

const (
	UserLevelInteract  UserLevel = 0
	UserLevelWatchOnly UserLevel = 1
)

type UserLevelRule struct {
	Pattern string
	Level   UserLevel

	matcher *regexp.Regexp
}

func ParseUserLevelRules(raw string) ([]UserLevelRule, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, fmt.Errorf("rules cannot be empty")
	}

	parts := strings.Split(trimmed, ",")
	rules := make([]UserLevelRule, 0, len(parts))
	for _, part := range parts {
		item := strings.TrimSpace(part)
		if item == "" {
			return nil, fmt.Errorf("invalid rule list: %q", raw)
		}

		sep := strings.LastIndex(item, "-")
		if sep <= 0 || sep >= len(item)-1 {
			return nil, fmt.Errorf("invalid rule %q (expected <pattern>-<level>)", item)
		}

		pattern := strings.TrimSpace(item[:sep])
		levelText := strings.TrimSpace(item[sep+1:])
		if pattern == "" || levelText == "" {
			return nil, fmt.Errorf("invalid rule %q (expected <pattern>-<level>)", item)
		}

		levelValue, err := strconv.Atoi(levelText)
		if err != nil {
			return nil, fmt.Errorf("invalid level %q in rule %q (expected 0 or 1)", levelText, item)
		}
		if levelValue != 0 && levelValue != 1 {
			return nil, fmt.Errorf("invalid level %q in rule %q (expected 0 or 1)", levelText, item)
		}

		matcher, err := compileUserLevelPattern(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid pattern %q in rule %q: %v", pattern, item, err)
		}

		rules = append(rules, UserLevelRule{
			Pattern: pattern,
			Level:   UserLevel(levelValue),
			matcher: matcher,
		})
	}

	if len(rules) == 0 {
		return nil, fmt.Errorf("rules cannot be empty")
	}

	return rules, nil
}

func MatchUserLevel(rules []UserLevelRule, ip string) (UserLevel, bool) {
	for _, rule := range rules {
		if rule.matcher != nil && rule.matcher.MatchString(ip) {
			return rule.Level, true
		}
	}
	return UserLevelInteract, false
}

func compileUserLevelPattern(pattern string) (*regexp.Regexp, error) {
	escaped := regexp.QuoteMeta(pattern)
	escaped = strings.ReplaceAll(escaped, "\\*", ".*")
	return regexp.Compile("^" + escaped + "$")
}
