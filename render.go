package main

// Human-facing rendering for the Plexus cockpit: a colored, blocked-first table (shared by
// `list` and `watch`) and the live `watch` TUI. Read-only — it only queries the server.

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/jjuanrivvera/presence/internal/store"
)

const (
	cReset = "\033[0m"
	cRed   = "\033[31m"
	cGreen = "\033[32m"
	cDim   = "\033[90m"
	cBold  = "\033[1m"
)

// stdoutIsTTY reports whether stdout is a terminal, so color is used interactively but never
// when the output is piped into a script.
func stdoutIsTTY() bool {
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func stateRank(s string) int {
	switch s {
	case "blocked":
		return 0 // needs you → floats to the top
	case "busy":
		return 1
	default:
		return 2
	}
}

func sortRows(rows []store.Session) {
	sort.SliceStable(rows, func(i, j int) bool {
		if ri, rj := stateRank(rows[i].State), stateRank(rows[j].State); ri != rj {
			return ri < rj
		}
		return rows[i].LastSeen > rows[j].LastSeen // newer first within a state
	})
}

// relTime renders an RFC3339 stamp as a compact age: 8s / 3m / 2h / 5d.
func relTime(ts string) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ts
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func short(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func rowColor(state string) string {
	switch state {
	case "blocked":
		return cRed + cBold
	case "busy":
		return cGreen
	default:
		return cDim
	}
}

func counts(rows []store.Session) (blocked, working, idle int) {
	for _, r := range rows {
		switch r.State {
		case "blocked":
			blocked++
		case "busy":
			working++
		default:
			idle++
		}
	}
	return
}

type column struct {
	head string
	get  func(store.Session) string
}

var tableCols = []column{
	{"SESSION", func(s store.Session) string { return short(s.SessionID, 12) }},
	{"HOST", func(s store.Session) string { return s.Host }},
	{"AGENT", func(s store.Session) string { return s.Agent }},
	{"REPO", func(s store.Session) string { return s.Repo }},
	{"PORT", func(s store.Session) string {
		if s.InjectPort > 0 {
			return fmt.Sprint(s.InjectPort)
		}
		return "-"
	}},
	{"STATE", func(s store.Session) string { return s.State }},
	{"SEEN", func(s store.Session) string { return relTime(s.LastSeen) }},
}

// renderTable formats rows as an aligned, blocked-first table. Widths are computed on plain
// text and color wraps whole (already-padded) rows, so ANSI never breaks column alignment.
func renderTable(rows []store.Session, color bool) string {
	sortRows(rows)
	w := make([]int, len(tableCols))
	for i, c := range tableCols {
		w[i] = len(c.head)
	}
	for _, r := range rows {
		for i, c := range tableCols {
			if l := len(c.get(r)); l > w[i] {
				w[i] = l
			}
		}
	}
	var b strings.Builder
	if color {
		b.WriteString(cDim)
	}
	for i, c := range tableCols {
		fmt.Fprintf(&b, "%-*s  ", w[i], c.head)
	}
	if color {
		b.WriteString(cReset)
	}
	b.WriteString("\n")
	for _, r := range rows {
		if color {
			b.WriteString(rowColor(r.State))
		}
		for i, c := range tableCols {
			fmt.Fprintf(&b, "%-*s  ", w[i], c.get(r))
		}
		if color {
			b.WriteString(cReset)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// cmdWatch is the live TUI: a full-screen, auto-refreshing view of the whole fleet. Read-only.
func cmdWatch(args []string) {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	interval := fs.Int("n", 2, "refresh interval seconds")
	fs.Parse(args) //nolint:errcheck // ExitOnError
	if *interval < 1 {
		*interval = 1
	}
	cc := newClientCtx("", "", false)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	fmt.Print("\033[?25l")              // hide cursor
	defer fmt.Print("\033[?25h\033[0m") // restore cursor + reset on exit

	draw := func() {
		rows, err := cc.cli.List("", "", "", "2m")
		fmt.Print("\033[H\033[2J") // cursor home + clear screen
		fmt.Printf("%spresence — the herd%s   %s   %s(refresh %ds · ctrl-c quits)%s\n\n",
			cBold, cReset, time.Now().Format("15:04:05"), cDim, *interval, cReset)
		if err != nil {
			fmt.Printf("  %sserver unreachable: %v%s\n", cRed, err, cReset)
			return
		}
		if len(rows) == 0 {
			fmt.Printf("  %sno sessions%s\n", cDim, cReset)
			return
		}
		b, wk, id := counts(rows)
		fmt.Printf("  %s⚑ %d blocked%s    %s● %d working%s    %s○ %d idle%s\n\n",
			cRed+cBold, b, cReset, cGreen, wk, cReset, cDim, id, cReset)
		fmt.Print("  " + strings.ReplaceAll(renderTable(rows, true), "\n", "\n  "))
	}

	draw()
	t := time.NewTicker(time.Duration(*interval) * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			draw()
		}
	}
}
