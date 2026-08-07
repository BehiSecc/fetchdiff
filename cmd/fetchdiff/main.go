package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/BehiSecc/fetchdiff/internal/app"
	"github.com/BehiSecc/fetchdiff/internal/config"
	"github.com/BehiSecc/fetchdiff/internal/fetch"
	"github.com/BehiSecc/fetchdiff/internal/model"
	"github.com/BehiSecc/fetchdiff/internal/notifier"
	"github.com/BehiSecc/fetchdiff/internal/schedule"
	"github.com/BehiSecc/fetchdiff/internal/store"
	"github.com/spf13/cobra"
)

const version = "0.1.0-dev"

type cli struct {
	out       io.Writer
	errOut    io.Writer
	dataDir   string
	timeout   time.Duration
	retries   int
	redirects int
	userAgent string
}

type runtime struct {
	service       *app.Service
	store         *store.Store
	paths         config.Paths
	notifications *notifier.Client
	dispatcher    *notifier.Dispatcher
	notifyErr     error
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	command := newRootCommand(os.Stdout, os.Stderr)
	if err := command.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func newRootCommand(out, errOut io.Writer) *cobra.Command {
	c := &cli{out: out, errOut: errOut, timeout: fetch.DefaultTimeout, retries: fetch.DefaultMaxRetries, redirects: fetch.DefaultMaxRedirects, userAgent: fetch.DefaultUserAgent}
	root := &cobra.Command{
		Use:           "fetchdiff",
		Short:         "Monitor remote JavaScript and web pages for changes",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(out)
	root.SetErr(errOut)
	root.PersistentFlags().StringVar(&c.dataDir, "data-dir", "", "state directory (default ~/.fetchdiff)")
	root.PersistentFlags().DurationVar(&c.timeout, "timeout", fetch.DefaultTimeout, "HTTP request timeout")
	root.PersistentFlags().IntVar(&c.retries, "max-retries", fetch.DefaultMaxRetries, "maximum retries after a transient failure")
	root.PersistentFlags().IntVar(&c.redirects, "max-redirects", fetch.DefaultMaxRedirects, "maximum redirects per request")
	root.PersistentFlags().StringVar(&c.userAgent, "user-agent", fetch.DefaultUserAgent, "HTTP User-Agent header")
	root.AddCommand(
		c.addCommand(),
		c.checkCommand(),
		c.watchCommand(),
		c.listCommand(),
		c.historyCommand(),
		c.diffCommand(),
		c.statusCommand(),
		c.doctorCommand(),
		c.notifyTestCommand(),
	)
	return root
}

func (c *cli) open() (*runtime, error) {
	if c.retries < 0 {
		return nil, errors.New("--max-retries cannot be negative")
	}
	if c.redirects < 1 {
		return nil, errors.New("--max-redirects must be at least one")
	}
	if c.timeout <= 0 {
		return nil, errors.New("--timeout must be greater than zero")
	}
	if strings.TrimSpace(c.userAgent) == "" {
		return nil, errors.New("--user-agent cannot be empty")
	}
	paths, err := config.ResolvePaths(c.dataDir)
	if err != nil {
		return nil, err
	}
	state := store.New(paths)
	if err := state.Initialize(); err != nil {
		return nil, err
	}
	notificationClient, notifyErr := notifier.Load(paths.Providers)
	var destinationKeys []string
	if notifyErr == nil {
		destinationKeys = notificationClient.Keys()
	}
	fetcher := fetch.New(fetch.Options{Timeout: c.timeout, MaxRetries: c.retries, DisableRetries: c.retries == 0, MaxRedirects: c.redirects, UserAgent: c.userAgent})
	runtime := &runtime{
		service: app.New(state, fetcher, destinationKeys...), store: state, paths: paths,
		notifications: notificationClient, notifyErr: notifyErr,
	}
	if notificationClient != nil {
		runtime.dispatcher = notifier.NewDispatcher(notificationClient, state)
	}
	return runtime, nil
}

func (r *runtime) validateNotifications() error {
	if r.notifyErr != nil {
		return fmt.Errorf("notification configuration: %w", r.notifyErr)
	}
	return nil
}

func (r *runtime) drain(ctx context.Context) notifier.DispatchReport {
	if r.dispatcher == nil {
		return notifier.DispatchReport{}
	}
	return r.dispatcher.Drain(ctx)
}

func (c *cli) addCommand() *cobra.Command {
	var name string
	var every time.Duration
	var rawHeaders []string
	command := &cobra.Command{
		Use:   "add URL",
		Short: "Fetch a URL and save its initial baseline",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			headers, err := parseHeaders(rawHeaders)
			if err != nil {
				return err
			}
			runtime, err := c.open()
			if err != nil {
				return err
			}
			target, err := runtime.service.Add(cmd.Context(), app.AddInput{Name: name, URL: args[0], Every: every, Headers: headers})
			if err != nil {
				return err
			}
			printBaseline(c.out, target)
			return nil
		},
	}
	command.Flags().StringVar(&name, "name", "", "unique target name")
	command.Flags().DurationVar(&every, "every", 24*time.Hour, "check interval")
	command.Flags().StringArrayVar(&rawHeaders, "header", nil, "request header in 'Name: value' form (repeatable)")
	_ = command.MarkFlagRequired("name")
	return command
}

func (c *cli) checkCommand() *cobra.Command {
	var force bool
	command := &cobra.Command{
		Use:   "check [name]",
		Short: "Check due targets once and exit",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			runtime, err := c.open()
			if err != nil {
				return err
			}
			if err := runtime.validateNotifications(); err != nil {
				return err
			}
			if force && len(args) == 0 {
				return errors.New("--force requires a target name")
			}
			before := runtime.drain(cmd.Context())
			if len(args) == 1 {
				result, checkErr := runtime.service.CheckTarget(cmd.Context(), args[0], force)
				if result.Skipped {
					fmt.Fprintf(c.out, "%s is not due; next check %s\n", result.Target.Name, formatTime(result.Target.NextCheckAt))
					renderDispatch(c.out, before)
					return before.Err()
				}
				renderResult(c.out, runtime.service, result)
				after := runtime.drain(cmd.Context())
				renderDispatch(c.out, before, after)
				return errors.Join(checkErr, before.Err(), after.Err())
			}
			results, checkErr := runtime.service.CheckDue(cmd.Context())
			if len(results) == 0 {
				fmt.Fprintln(c.out, "No targets are due.")
				renderDispatch(c.out, before)
				return errors.Join(checkErr, before.Err())
			}
			for _, result := range results {
				renderResult(c.out, runtime.service, result)
			}
			after := runtime.drain(cmd.Context())
			renderDispatch(c.out, before, after)
			return errors.Join(checkErr, before.Err(), after.Err())
		},
	}
	command.Flags().BoolVar(&force, "force", false, "check now even when the target is not due")
	return command
}

func (c *cli) watchCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "watch",
		Short: "Continuously check targets when they become due",
		RunE: func(cmd *cobra.Command, _ []string) error {
			runtime, err := c.open()
			if err != nil {
				return err
			}
			if err := runtime.validateNotifications(); err != nil {
				return err
			}
			fmt.Fprintf(c.out, "Watching targets. State: %s\n", runtime.paths.Root)
			for {
				before := runtime.drain(cmd.Context())
				renderDispatch(c.out, before)
				if cmd.Context().Err() != nil {
					fmt.Fprintln(c.out, "Watcher stopped.")
					return nil
				}
				results, checkErr := runtime.service.CheckDue(cmd.Context())
				hasOperationalError := false
				for _, result := range results {
					if result.Target.ID == "" {
						hasOperationalError = true
						continue
					}
					if shouldRenderWatch(result) {
						renderResult(c.out, runtime.service, result)
					}
				}
				if checkErr != nil && hasOperationalError && !errors.Is(checkErr, context.Canceled) {
					fmt.Fprintln(c.errOut, "Check error:", checkErr)
				}
				after := runtime.drain(cmd.Context())
				renderDispatch(c.out, after)
				if err := cmd.Context().Err(); err != nil {
					fmt.Fprintln(c.out, "Watcher stopped.")
					return nil
				}
				targets, err := runtime.service.Targets()
				if err != nil {
					return err
				}
				wait := schedule.NextWake(targets, time.Now().UTC(), 30*time.Second)
				if wait < 250*time.Millisecond {
					wait = 250 * time.Millisecond
				}
				timer := time.NewTimer(wait)
				select {
				case <-cmd.Context().Done():
					timer.Stop()
					fmt.Fprintln(c.out, "Watcher stopped.")
					return nil
				case <-timer.C:
				}
			}
		},
	}
}

func (c *cli) listCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List monitored targets",
		RunE: func(_ *cobra.Command, _ []string) error {
			runtime, err := c.open()
			if err != nil {
				return err
			}
			targets, err := runtime.service.Targets()
			if err != nil {
				return err
			}
			if len(targets) == 0 {
				fmt.Fprintln(c.out, "No targets configured.")
				return nil
			}
			writer := tabwriter.NewWriter(c.out, 0, 4, 2, ' ', 0)
			fmt.Fprintln(writer, "NAME\tTYPE\tEVERY\tHEALTH\tLAST CHECK\tNEXT CHECK\tURL")
			for _, target := range targets {
				fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", target.Name, target.ResourceType, target.Every, health(target), formatTime(target.LastCheckedAt), formatTime(target.NextCheckAt), target.URL)
			}
			return writer.Flush()
		},
	}
}

func (c *cli) historyCommand() *cobra.Command {
	var limit int
	command := &cobra.Command{
		Use:   "history NAME",
		Short: "Show a target's check history",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			runtime, err := c.open()
			if err != nil {
				return err
			}
			entries, err := runtime.service.History(args[0])
			if err != nil {
				return err
			}
			if limit > 0 && len(entries) > limit {
				entries = entries[:limit]
			}
			writer := tabwriter.NewWriter(c.out, 0, 4, 2, ' ', 0)
			fmt.Fprintln(writer, "CHECKED\tOUTCOME\tSTATUS\tSIZE\tHASH\tDETAIL")
			for _, entry := range entries {
				detail := entry.Error
				if entry.StatusChanged {
					detail = appendDetail(detail, fmt.Sprintf("status %d → %d", entry.PreviousStatusCode, entry.StatusCode))
				}
				if entry.RedirectChanged {
					detail = appendDetail(detail, fmt.Sprintf("redirect %s → %s", entry.PreviousURL, entry.EffectiveURL))
				}
				fmt.Fprintf(writer, "%s\t%s\t%d\t%s\t%s\t%s\n", formatTime(entry.CheckedAt), entry.Outcome, entry.StatusCode, formatBytes(entry.Size), shortHash(entry.Hash), detail)
			}
			return writer.Flush()
		},
	}
	command.Flags().IntVar(&limit, "limit", 20, "maximum history entries to show (0 for all)")
	return command
}

func (c *cli) diffCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "diff NAME",
		Short: "Show the full diff between the latest two snapshots",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			runtime, err := c.open()
			if err != nil {
				return err
			}
			revision, err := runtime.service.RevisionDiff(args[0])
			if err != nil {
				return err
			}
			printChange(c.out, revision)
			return nil
		},
	}
}

func (c *cli) statusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Summarize monitor state",
		RunE: func(_ *cobra.Command, _ []string) error {
			runtime, err := c.open()
			if err != nil {
				return err
			}
			targets, err := runtime.service.Targets()
			if err != nil {
				return err
			}
			now := time.Now().UTC()
			due, failing := 0, 0
			for _, target := range targets {
				if schedule.Due(target, now) {
					due++
				}
				if target.ConsecutiveFailures > 0 {
					failing++
				}
			}
			_, pending, err := runtime.store.NotificationCounts()
			if err != nil {
				return err
			}
			notificationStatus := "disabled"
			if runtime.notifyErr != nil {
				notificationStatus = "invalid (run fetchdiff doctor)"
			} else if runtime.notifications.Count() > 0 {
				notificationStatus = fmt.Sprintf("enabled (%d destinations)", runtime.notifications.Count())
			}
			fmt.Fprintf(c.out, "Targets: %d\nDue: %d\nFailing: %d\nNotifications: %s\nPending deliveries: %d\nState: %s\n", len(targets), due, failing, notificationStatus, pending, runtime.paths.Root)
			return nil
		},
	}
}

func (c *cli) doctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check storage and snapshot integrity",
		RunE: func(_ *cobra.Command, _ []string) error {
			runtime, err := c.open()
			if err != nil {
				return err
			}
			if err := doctor(runtime); err != nil {
				return err
			}
			notificationStatus := "disabled (edit " + runtime.paths.Providers + ")"
			if runtime.notifications.Count() > 0 {
				notificationStatus = fmt.Sprintf("valid (%d destinations)", runtime.notifications.Count())
			}
			fmt.Fprintf(c.out, "✓ Data directory is writable and private\n✓ State database is readable\n✓ Snapshots are present and valid\n✓ Provider configuration is %s\n\nState: %s\n", notificationStatus, runtime.paths.Root)
			return nil
		},
	}
}

func (c *cli) notifyTestCommand() *cobra.Command {
	var providers []string
	var ids []string
	command := &cobra.Command{
		Use:   "notify-test",
		Short: "Send a test through configured notification providers",
		RunE: func(cmd *cobra.Command, _ []string) error {
			runtime, err := c.open()
			if err != nil {
				return err
			}
			if err := runtime.validateNotifications(); err != nil {
				return err
			}
			if runtime.notifications.Count() == 0 {
				return fmt.Errorf("no notification providers configured; edit %s", runtime.paths.Providers)
			}
			filter := notifier.Filter{Providers: providers, IDs: ids}
			if len(runtime.notifications.Select(filter)) == 0 {
				return errors.New("no notification destination matches the selected provider or id")
			}
			message := notifier.Message{
				Text: fmt.Sprintf("✅ FetchDiff notification test\n\nProvider delivery is working.\nChecked: %s", formatTime(time.Now().UTC())),
				Data: map[string]string{"event": "test", "name": "notification-test", "checked": time.Now().UTC().Format(time.RFC3339)},
			}
			results := runtime.notifications.SendAll(cmd.Context(), message, filter)
			var failures []error
			for _, result := range results {
				if result.Err != nil {
					fmt.Fprintf(c.out, "✗ %s: %v\n", result.Key, result.Err)
					failures = append(failures, fmt.Errorf("%s: %w", result.Key, result.Err))
					continue
				}
				fmt.Fprintf(c.out, "✓ %s\n", result.Key)
			}
			return errors.Join(failures...)
		},
	}
	command.Flags().StringSliceVar(&providers, "provider", nil, "provider type to test, such as discord or telegram")
	command.Flags().StringSliceVar(&ids, "id", nil, "configured provider id to test")
	return command
}

func renderResult(out io.Writer, service *app.Service, result app.CheckResult) {
	if result.History.Outcome == model.OutcomeFailure {
		fmt.Fprintf(out, "✗ %s check failed (%d consecutive): %s\n", result.Target.Name, result.Target.ConsecutiveFailures, result.History.Error)
		if result.FailureReached {
			fmt.Fprintln(out, "  Failure threshold reached.")
		}
		return
	}
	if result.Changed {
		revision, err := service.RevisionDiff(result.Target.Name)
		if err != nil {
			fmt.Fprintf(out, "🔄 %s changed · %s\nDiff unavailable: %v\n", result.Target.ResourceType, result.Target.Name, err)
			return
		}
		printChange(out, revision)
		return
	}
	if result.Recovered {
		fmt.Fprintf(out, "✓ %s recovered and is unchanged.\n", result.Target.Name)
		return
	}
	if result.History.RedirectChanged || result.History.StatusChanged {
		fmt.Fprintf(out, "↪ %s metadata changed", result.Target.Name)
		if result.History.StatusChanged {
			fmt.Fprintf(out, " · status %d → %d", result.History.PreviousStatusCode, result.History.StatusCode)
		}
		if result.History.RedirectChanged {
			fmt.Fprintf(out, " · redirect %s → %s", result.History.PreviousURL, result.History.EffectiveURL)
		}
		fmt.Fprintln(out)
		return
	}
	fmt.Fprintf(out, "✓ %s unchanged.\n", result.Target.Name)
}

func shouldRenderWatch(result app.CheckResult) bool {
	if result.History.Outcome == model.OutcomeFailure {
		return result.FailureReached
	}
	return result.Changed || result.Recovered || result.History.StatusChanged || result.History.RedirectChanged
}

func renderDispatch(out io.Writer, reports ...notifier.DispatchReport) {
	delivered := 0
	pending := 0
	var failures []error
	for _, report := range reports {
		delivered += report.Delivered
		pending = report.Pending
		failures = append(failures, report.Errors...)
	}
	if delivered > 0 {
		fmt.Fprintf(out, "✓ Notifications sent: %d\n", delivered)
	}
	if len(failures) > 0 {
		fmt.Fprintf(out, "⚠ Notifications pending: %d (will retry)\n", pending)
		for _, err := range failures {
			fmt.Fprintf(out, "  %v\n", err)
		}
	}
}

func printChange(out io.Writer, revision app.RevisionDiff) {
	delta := revision.Current.Size - revision.Previous.Size
	fmt.Fprintf(out, "🔄 %s changed · %s\n\n%s\n\n", revision.Target.ResourceType, revision.Target.Name, revision.Target.URL)
	fmt.Fprintf(out, "Size: %s → %s (%s)\n", formatBytes(revision.Previous.Size), formatBytes(revision.Current.Size), signedBytes(delta))
	fmt.Fprintf(out, "Lines: +%d / -%d\n", revision.Diff.Added, revision.Diff.Removed)
	fmt.Fprintf(out, "Hash: %s → %s\n", shortHash(revision.Previous.Hash), shortHash(revision.Current.Hash))
	fmt.Fprintf(out, "Status: %d %s\n", revision.Current.StatusCode, httpStatusText(revision.Current.StatusCode))
	if revision.Current.StatusChanged {
		fmt.Fprintf(out, "Status change: %d → %d\n", revision.Current.PreviousStatusCode, revision.Current.StatusCode)
	}
	if revision.Current.RedirectChanged {
		fmt.Fprintf(out, "Redirect: %s → %s\n", revision.Current.PreviousURL, revision.Current.EffectiveURL)
	}
	if revision.Current.Recovered {
		fmt.Fprintln(out, "Recovery: target is healthy again")
	}
	fmt.Fprintf(out, "Checked: %s\n", formatTime(revision.Current.CheckedAt))
	if revision.Diff.FormatNote != "" {
		fmt.Fprintf(out, "Note: %s\n", revision.Diff.FormatNote)
	}
	fmt.Fprintln(out)
	fmt.Fprint(out, revision.Diff.Text)
	if !strings.HasSuffix(revision.Diff.Text, "\n") {
		fmt.Fprintln(out)
	}
}

func printBaseline(out io.Writer, target model.Target) {
	fmt.Fprintf(out, "✓ Baseline created\n\nTarget: %s\nType: %s\nStatus: %d\nSize: %s\nSHA-256: %s\nNext check: %s\n", target.Name, target.ResourceType, target.StatusCode, formatBytes(target.SnapshotSize), shortHash(target.SnapshotHash), formatTime(target.NextCheckAt))
}

func parseHeaders(values []string) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	headers := make(map[string]string, len(values))
	for _, raw := range values {
		name, value, ok := strings.Cut(raw, ":")
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		if !ok || name == "" || value == "" || strings.ContainsAny(name, "\r\n") || strings.ContainsAny(value, "\r\n") {
			return nil, fmt.Errorf("invalid header %q; use 'Name: value'", raw)
		}
		headers[name] = value
	}
	return headers, nil
}

func doctor(runtime *runtime) error {
	for _, path := range []string{runtime.paths.Root, runtime.paths.Snapshots} {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if info.Mode().Perm()&0o077 != 0 {
			return fmt.Errorf("%s permissions are too broad: %o", path, info.Mode().Perm())
		}
	}
	for _, path := range []string{runtime.paths.Database, runtime.paths.Providers} {
		if info, err := os.Stat(path); err != nil {
			return err
		} else if info.Mode().Perm()&0o077 != 0 {
			return fmt.Errorf("%s permissions are too broad: %o", path, info.Mode().Perm())
		}
	}
	if err := runtime.validateNotifications(); err != nil {
		return err
	}
	targets, err := runtime.service.Targets()
	if err != nil {
		return err
	}
	checked := make(map[string]bool)
	for _, target := range targets {
		entries, err := runtime.service.History(target.Name)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.Hash == "" || checked[entry.Hash] {
				continue
			}
			content, err := runtime.store.Snapshot(entry.Hash)
			if err != nil {
				return fmt.Errorf("%s snapshot %s: %w", target.Name, shortHash(entry.Hash), err)
			}
			actual := fmt.Sprintf("%x", sha256.Sum256(content))
			if actual != entry.Hash {
				return fmt.Errorf("%s snapshot %s hash mismatch", target.Name, shortHash(entry.Hash))
			}
			checked[entry.Hash] = true
		}
	}
	notifications, err := runtime.store.Notifications()
	if err != nil {
		return err
	}
	for _, notification := range notifications {
		for destination := range notification.Deliveries {
			if !runtime.notifications.Has(destination) {
				return fmt.Errorf("pending notification references removed destination %q", destination)
			}
		}
	}
	return nil
}

func formatBytes(size int64) string {
	const unit = int64(1024)
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	value := float64(size)
	units := []string{"KB", "MB", "GB"}
	for _, suffix := range units {
		value /= 1024
		if value < 1024 || suffix == "GB" {
			return fmt.Sprintf("%.1f %s", value, suffix)
		}
	}
	return strconv.FormatInt(size, 10) + " B"
}

func signedBytes(size int64) string {
	if size > 0 {
		return "+" + formatBytes(size)
	}
	if size < 0 {
		return "-" + formatBytes(-size)
	}
	return "0 B"
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return "never"
	}
	return value.UTC().Format("2006-01-02 15:04 UTC")
}

func shortHash(hash string) string {
	if len(hash) <= 8 {
		return hash
	}
	return hash[:8]
}

func health(target model.Target) string {
	if target.ConsecutiveFailures > 0 {
		return fmt.Sprintf("failing (%d)", target.ConsecutiveFailures)
	}
	return "healthy"
}

func appendDetail(existing, next string) string {
	if existing == "" {
		return next
	}
	return existing + "; " + next
}

func httpStatusText(code int) string {
	return http.StatusText(code)
}
