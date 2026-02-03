package server

import "strings"

// ExpandBindPatterns replaces wildcard patterns (containing '*') with matching
// local IPv4 addresses. Patterns that match nothing are removed.
func ExpandBindPatterns(patterns []string) []string {
	localIPs := LocalIPv4s()
	seen := make(map[string]struct{}, len(patterns))
	out := make([]string, 0, len(patterns))

	for _, pattern := range patterns {
		cleaned := strings.TrimSpace(pattern)
		if cleaned == "" {
			continue
		}

		if strings.Contains(cleaned, "*") {
			matcher, err := compileUserLevelPattern(cleaned)
			if err != nil {
				continue
			}
			for _, ip := range localIPs {
				if matcher.MatchString(ip) {
					if _, ok := seen[ip]; ok {
						continue
					}
					seen[ip] = struct{}{}
					out = append(out, ip)
				}
			}
			continue
		}

		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		out = append(out, cleaned)
	}

	return out
}

