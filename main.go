// Command ddnscheck is the independent efficacy monitor for dynamic
// DNS (docs/MONITORING.md, ddns phase 2): it answers "does the
// published record actually point at our WAN egress?" via paths
// DELIBERATELY DISJOINT from ddns-updater's own machinery, so the two
// cannot be wrong together.
//
//   - The published record is resolved against a PUBLIC resolver
//     (default 1.1.1.1:53), dialed directly -- never through CoreDNS,
//     whose split horizon answers 172.16.42.3 for the same name.
//   - The WAN egress IP comes from HTTPS what-is-my-ip services,
//     NOT from the DNS-based fetchers (whoami.cloudflare /
//     myip.opendns.com) that ddns-updater itself uses.
//
// It exposes the verdict as Prometheus metrics on --port; the
// homenet_metrics bridge carries the scrape. A mismatch sustained
// past the failover-convergence budget pages (rules in
// monitoring/rules/homenet-ddns.yml). Zero dependencies, stdlib only,
// FROM scratch (see Dockerfile).
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"
)

type checker struct {
	record  string
	wanURLs []string
	res     *net.Resolver
	client  *http.Client
	timeout time.Duration

	mu sync.Mutex
	st state
}

// state is the latest snapshot the /metrics handler serves. matches,
// publishedIP and wanIP survive failed checks (last known verdict);
// lastSuccess going stale is its own alertable signal.
type state struct {
	haveVerdict   bool
	matches       bool
	publishedIP   string
	wanIP         string
	lastCheckOK   bool
	lastSuccess   time.Time
	checks        uint64
	resolveErrors uint64
	wanErrors     uint64
}

func main() {
	fs := flag.NewFlagSet("ddnscheck", flag.ContinueOnError)
	port := fs.Int("port", 9878, "metrics listener port")
	record := fs.String("record", "home.stewart.net", "DNS record to verify")
	resolver := fs.String("resolver", "1.1.1.1:53", "public DNS resolver (host:port) -- must NOT be the split-horizon CoreDNS")
	wanURLs := fs.String("wan-urls", "https://checkip.amazonaws.com,https://api.ipify.org", "comma-separated HTTPS services echoing the caller's IP, tried in order")
	interval := fs.Duration("interval", 2*time.Minute, "time between checks")
	timeout := fs.Duration("timeout", 15*time.Second, "per-check deadline")
	if err := fs.Parse(os.Args[1:]); err != nil {
		if err == flag.ErrHelp {
			os.Exit(0)
		}
		os.Exit(2) // flag already printed the message and usage
	}

	c := &checker{
		record:  *record,
		wanURLs: splitURLs(*wanURLs),
		timeout: *timeout,
		client:  &http.Client{Timeout: *timeout},
		res: &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, network, *resolver)
			},
		},
	}

	go func() {
		c.check()
		for range time.Tick(*interval) {
			c.check()
		}
	}()

	http.HandleFunc("/metrics", c.metrics)
	http.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ddnscheck -- see /metrics")
	})
	log.Printf("ddnscheck: verifying %s against %s + %s every %v, listening on :%d",
		*record, *resolver, *wanURLs, *interval, *port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", *port), nil))
}

func splitURLs(s string) []string {
	var out []string
	for _, u := range strings.Split(s, ",") {
		if u = strings.TrimSpace(u); u != "" {
			out = append(out, u)
		}
	}
	return out
}

func (c *checker) check() {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	published, resolveErr := c.lookupRecord(ctx)
	wan, wanErr := c.lookupWAN(ctx)

	c.mu.Lock()
	defer c.mu.Unlock()
	c.st.checks++
	if resolveErr != nil {
		c.st.resolveErrors++
		log.Printf("resolve %s: %v", c.record, resolveErr)
	}
	if wanErr != nil {
		c.st.wanErrors++
		log.Printf("wan ip: %v", wanErr)
	}
	c.st.lastCheckOK = resolveErr == nil && wanErr == nil
	if !c.st.lastCheckOK {
		return
	}
	matches := published == wan
	if !c.st.haveVerdict || matches != c.st.matches ||
		published.String() != c.st.publishedIP || wan.String() != c.st.wanIP {
		log.Printf("verdict: published=%s wan=%s match=%v", published, wan, matches)
	}
	c.st.haveVerdict = true
	c.st.matches = matches
	c.st.publishedIP = published.String()
	c.st.wanIP = wan.String()
	c.st.lastSuccess = time.Now()
}

// lookupRecord returns the record's IPv4 answer from the public
// resolver. Multiple A records would mean something is deeply wrong
// with a single-homed dynamic record, so that is an error, not a
// "pick one".
func (c *checker) lookupRecord(ctx context.Context) (netip.Addr, error) {
	addrs, err := c.res.LookupNetIP(ctx, "ip4", c.record)
	if err != nil {
		return netip.Addr{}, err
	}
	if len(addrs) != 1 {
		return netip.Addr{}, fmt.Errorf("want exactly 1 A record, got %v", addrs)
	}
	return addrs[0].Unmap(), nil
}

// lookupWAN asks each echo service in order and returns the first
// parseable IPv4 answer.
func (c *checker) lookupWAN(ctx context.Context) (netip.Addr, error) {
	var lastErr error
	for _, u := range c.wanURLs {
		addr, err := c.fetchIP(ctx, u)
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", u, err)
			continue
		}
		return addr, nil
	}
	return netip.Addr{}, lastErr
}

func (c *checker) fetchIP(ctx context.Context, url string) (netip.Addr, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return netip.Addr{}, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return netip.Addr{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return netip.Addr{}, fmt.Errorf("status %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	if err != nil {
		return netip.Addr{}, err
	}
	return parseIPv4(string(body))
}

func parseIPv4(body string) (netip.Addr, error) {
	addr, err := netip.ParseAddr(strings.TrimSpace(body))
	if err != nil {
		return netip.Addr{}, err
	}
	addr = addr.Unmap()
	if !addr.Is4() {
		return netip.Addr{}, fmt.Errorf("not IPv4: %s", addr)
	}
	return addr, nil
}

func (c *checker) metrics(w http.ResponseWriter, _ *http.Request) {
	c.mu.Lock()
	st := c.st
	c.mu.Unlock()

	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	b := &strings.Builder{}

	fmt.Fprintf(b, "# HELP homenet_ddns_record_matches_wan 1 when the record's public answer equals the WAN egress IP.\n")
	fmt.Fprintf(b, "# TYPE homenet_ddns_record_matches_wan gauge\n")
	if st.haveVerdict {
		fmt.Fprintf(b, "homenet_ddns_record_matches_wan{record=%q} %d\n", c.record, boolInt(st.matches))
		fmt.Fprintf(b, "# HELP homenet_ddns_published_ip The record's current public-DNS answer (info-style; value always 1).\n")
		fmt.Fprintf(b, "# TYPE homenet_ddns_published_ip gauge\n")
		fmt.Fprintf(b, "homenet_ddns_published_ip{record=%q,ip=%q} 1\n", c.record, st.publishedIP)
		fmt.Fprintf(b, "# HELP homenet_ddns_wan_ip The current WAN egress IP (info-style; value always 1).\n")
		fmt.Fprintf(b, "# TYPE homenet_ddns_wan_ip gauge\n")
		fmt.Fprintf(b, "homenet_ddns_wan_ip{ip=%q} 1\n", st.wanIP)
	}

	fmt.Fprintf(b, "# HELP homenet_ddns_last_check_success 1 when the most recent check completed both lookups.\n")
	fmt.Fprintf(b, "# TYPE homenet_ddns_last_check_success gauge\n")
	fmt.Fprintf(b, "homenet_ddns_last_check_success %d\n", boolInt(st.lastCheckOK))

	fmt.Fprintf(b, "# HELP homenet_ddns_last_success_timestamp_seconds Unix time of the last fully successful check (0 = never).\n")
	fmt.Fprintf(b, "# TYPE homenet_ddns_last_success_timestamp_seconds gauge\n")
	var ts int64
	if !st.lastSuccess.IsZero() {
		ts = st.lastSuccess.Unix()
	}
	fmt.Fprintf(b, "homenet_ddns_last_success_timestamp_seconds %d\n", ts)

	fmt.Fprintf(b, "# HELP homenet_ddns_checks_total Check attempts.\n")
	fmt.Fprintf(b, "# TYPE homenet_ddns_checks_total counter\n")
	fmt.Fprintf(b, "homenet_ddns_checks_total %d\n", st.checks)

	fmt.Fprintf(b, "# HELP homenet_ddns_check_errors_total Failed lookups by stage.\n")
	fmt.Fprintf(b, "# TYPE homenet_ddns_check_errors_total counter\n")
	fmt.Fprintf(b, "homenet_ddns_check_errors_total{stage=\"resolve\"} %d\n", st.resolveErrors)
	fmt.Fprintf(b, "homenet_ddns_check_errors_total{stage=\"wan\"} %d\n", st.wanErrors)

	io.WriteString(w, b.String())
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
