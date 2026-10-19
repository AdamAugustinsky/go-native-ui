//go:build !darwin && !windows

package locale

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Linux and the other Unix systems: the process's environment, read once.

const volatile = false

func watch() {}

func read() Info { return readEnv(os.Getenv, localeTimeFormat) }

// readEnv derives the settings from the environment variables of POSIX
// locales. timeFmt returns the locale's time format (t_fmt) for the variable
// values in force.
func readEnv(getenv func(string) string, timeFmt func() string) Info {
	var info Info
	seen := map[string]bool{}
	add := func(s string) {
		if tag := normalizeTag(s); tag != "" && !seen[tag] {
			seen[tag] = true
			info.Languages = append(info.Languages, tag)
		}
	}
	// LANGUAGE is a colon-separated preference list that gettext honours; the
	// locale variables follow, in their order of precedence.
	for _, l := range strings.Split(getenv("LANGUAGE"), ":") {
		add(l)
	}
	first := func(names ...string) string {
		for _, n := range names {
			if v := getenv(n); v != "" {
				return v
			}
		}
		return ""
	}
	add(first("LC_ALL", "LC_MESSAGES", "LANG"))
	info.Region = regionOf(normalizeTag(first("LC_ALL", "LC_TIME", "LANG")))
	if info.Region == "" && len(info.Languages) > 0 {
		info.Region = regionOf(info.Languages[0])
	}
	info.Hour24, _ = hour24FromPattern(timeFmt())
	return info
}

// localeTimeFormat asks the locale command for t_fmt, which is "%T" or "%H:%M:%S"
// for a 24-hour locale and "%r" or "%I:%M:%S %p" for a 12-hour one. It returns ""
// when the command is missing or fails.
func localeTimeFormat() string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "locale", "t_fmt").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
