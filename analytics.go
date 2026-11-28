package main

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"

	"github.com/spf13/cobra"
)

// chauffeur sapi analytics|audit and lobby analytics|audit. Analytics are the platform's minute rollups for the game over
// its plan's history window; the audit log is every change made to the game's Scripted API or lobby data.

var (
	auditLimit  int
	auditBefore string
	auditCursor = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)
)

func analyticsCmd(use, short, path string, print func(map[string]any)) *cobra.Command {
	return &cobra.Command{
		Use: use, Short: short, Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return sapiGet(cmd, path, print) },
	}
}

func auditCmd(short, base string) *cobra.Command {
	c := &cobra.Command{
		Use: "audit", Short: short, Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := auditPath(base, auditLimit, auditBefore)
			if err != nil {
				return err
			}
			return sapiGet(cmd, path, printAudit)
		},
	}
	c.Flags().IntVar(&auditLimit, "limit", 0, "Most entries to return, 1 to 200 (default 50)")
	c.Flags().StringVar(&auditBefore, "before", "", "The next_before of the previous page, to read older entries")
	return c
}

func auditPath(base string, limit int, before string) (string, error) {
	q := url.Values{}
	if limit != 0 {
		if limit < 1 || limit > 200 {
			return "", usageErrorf("--limit is a number from 1 to 200")
		}
		q.Set("limit", strconv.Itoa(limit))
	}
	if before != "" {
		if !auditCursor.MatchString(before) {
			return "", usageErrorf("--before is the next_before of the previous page")
		}
		q.Set("before", before)
	}
	path := base + "/audit"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	return path, nil
}

func init() {
	sapiCmd.AddCommand(
		analyticsCmd("analytics", "Show how the Scripted API is used: calls, errors, refusals and latency", "/tool/sapi/analytics", printSapiAnalytics),
		auditCmd("Show every change made to the Scripted API, newest first", "/tool/sapi"),
	)
	lobbyCmd.AddCommand(
		analyticsCmd("analytics", "Show how lobbies are used: opens, players, refusals at the plan's caps and TURN use", lobbyDataBase+"/analytics", printLobbyAnalytics),
		auditCmd("Show every change made to lobby data, newest first", lobbyDataBase),
	)
}

func num(v any) string {
	switch n := v.(type) {
	case float64:
		if n == float64(int64(n)) {
			return strconv.FormatInt(int64(n), 10)
		}
		return strconv.FormatFloat(n, 'f', 1, 64)
	case nil:
		return "0"
	}
	return fmt.Sprint(v)
}

func printSapiAnalytics(d map[string]any) {
	t, _ := d["totals"].(map[string]any)
	logf("plan %v  since %v\n", d["plan"], d["since"])
	logf("calls %s  errors %s  refused %s (quota %s, rate %s, budget %s, feature off %s)\n",
		num(t["calls"]), num(t["errors"]), num(t["refused"]), num(t["quota_rejects"]), num(t["rate_limited"]), num(t["budget_exceeded"]), num(t["feature_disabled"]))
	logf("latency avg %s ms  p50 %s ms  p95 %s ms  max %s ms\n", num(t["avg_ms"]), num(t["p50_ms"]), num(t["p95_ms"]), num(t["max_ms"]))
	logf("data reads %s  writes %s  outbound calls %s  internal calls %s  scheduled %s\n",
		num(t["data_reads"]), num(t["data_writes"]), num(t["outbound_calls"]), num(t["internal_calls"]), num(t["scheduled"]))
	logf("calls your scripts made to Blazium %s  over the log-only internal limit %s\n",
		num(t["script_internal_calls"]), num(t["internal_would_reject"]))
	logs, _ := d["logics"].([]any)
	if len(logs) > 0 {
		logf("by logic:\n")
		for _, v := range logs {
			l, _ := v.(map[string]any)
			logf("  %-20v calls %-8s errors %-6s refused %s\n", l["logic"], num(l["calls"]), num(l["errors"]), num(l["refused"]))
		}
	}
	days, _ := d["daily"].([]any)
	if len(days) > 0 {
		logf("by day (latest %d):\n", min(len(days), 7))
		for _, v := range days[max(0, len(days)-7):] {
			l, _ := v.(map[string]any)
			logf("  %-12v calls %-8s errors %-6s refused %s\n", l["day"], num(l["calls"]), num(l["errors"]), num(l["refused"]))
		}
	}
}

func printLobbyAnalytics(d map[string]any) {
	t, _ := d["totals"].(map[string]any)
	logf("plan %v  since %v\n", d["plan"], d["since"])
	if l, ok := d["limits"].(map[string]any); ok {
		printLobbyLimits(l)
	}
	logf("lobbies opened %s  closed %s  peak open %s  peak players %s  average life %s s\n",
		num(t["lobbies_created"]), num(t["lobbies_closed"]), num(t["peak_open_lobbies"]), num(t["peak_players"]), num(t["avg_lifetime_s"]))
	logf("joins %s  refused %s  leaves %s  kicks %s  messages %s\n",
		num(t["joins"]), num(t["join_refused"]), num(t["leaves"]), num(t["kicks"]), num(t["messages"]))
	logf("turned away at the plan's caps %s (lobbies %s, per host %s, players %s)  would reject %s\n",
		num(t["cap_rejected"]), num(t["lobby_cap_rejected"]), num(t["host_cap_rejected"]), num(t["players_cap_rejected"]), num(t["would_reject"]))
	logf("lobby scripts: %s calls, %s errors  data reads %s, writes %s, %s bytes\n",
		num(t["script_calls"]), num(t["script_errors"]), num(t["data_reads"]), num(t["data_writes"]), num(t["data_bytes"]))
	logf("calls your scripts made: %s third-party, %s to Blazium  over the log-only internal limit %s\n",
		num(t["outbound_calls"]), num(t["script_internal_calls"]), num(t["internal_would_reject"]))
}

func printLobbyLimits(l map[string]any) {
	logf("limits: %s open lobbies, %s players per lobby, %s per host, %s TURN allocations, %s bytes of data per lobby\n",
		num(l["lobbies_per_game"]), num(l["players_per_lobby"]), num(l["lobbies_per_host"]), num(l["turn_allocations"]), num(l["lobby_data_bytes"]))
}

func printAudit(d map[string]any) {
	entries, _ := d["entries"].([]any)
	if len(entries) == 0 {
		logf("No changes recorded.\n")
		return
	}
	for _, v := range entries {
		e, _ := v.(map[string]any)
		logf("%v  %-24v %-28v %v\n", e["created_at"], e["actor"], e["action"], e["detail"])
	}
	if next, ok := d["next_before"]; ok {
		logf("more: --before %s\n", num(next))
	}
}
