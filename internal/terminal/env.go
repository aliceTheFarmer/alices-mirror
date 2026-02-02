package terminal

import "strings"

func dropEnvVar(env []string, key string) []string {
	if key == "" {
		return env
	}
	prefix := key + "="
	out := make([]string, 0, len(env))
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			continue
		}
		out = append(out, item)
	}
	return out
}
