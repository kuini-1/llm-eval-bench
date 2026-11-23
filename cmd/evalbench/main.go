// Command evalbench runs prompt datasets across models, scores the answers
// and compares the results.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/kuini-1/llm-eval-bench/internal/config"
	"github.com/kuini-1/llm-eval-bench/internal/dataset"
	"github.com/kuini-1/llm-eval-bench/internal/report"
	"github.com/kuini-1/llm-eval-bench/internal/runner"
	"github.com/kuini-1/llm-eval-bench/internal/server"
	"github.com/kuini-1/llm-eval-bench/internal/store"
)

const usage = `evalbench: run a prompt dataset across models and compare them

Commands:
  serve                         start the API for the web UI
  import <file.jsonl|file.csv>  import a dataset   [-name] [-description]
  datasets                      list datasets
  run                           run a dataset      -dataset -models a,b,c [-judge] [-samples] [-name] [-out report.md]
  resume <run-id>               continue an interrupted run
  judge <run-id>                grade a finished run with a second judge   -judge [-samples]
  rescore <run-id>              re-apply the graders and scoring rules (no model calls)
  report <run-id>               print the comparison   [-format md|csv|results] [-out file]
  runs                          list runs

Every command takes -config (default: $EVALBENCH_CONFIG or bench.yaml).
`

func main() {
	if len(os.Args) < 2 || os.Args[1] == "help" || os.Args[1] == "-h" || os.Args[1] == "--help" {
		fmt.Print(usage)
		return
	}
	cmd, args := os.Args[1], os.Args[2:]
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var err error
	switch cmd {
	case "serve":
		err = serve(ctx, args)
	case "import":
		err = importCmd(ctx, args)
	case "datasets":
		err = datasets(ctx, args)
	case "run":
		err = run(ctx, args)
	case "resume":
		err = resume(ctx, args)
	case "judge":
		err = judge(ctx, args)
	case "report":
		err = reportCmd(ctx, args)
	case "rescore":
		err = rescore(ctx, args)
	case "runs":
		err = runs(ctx, args)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

type env struct {
	cfg *config.Config
	st  *store.Store
	rn  *runner.Runner
}

// setup parses flags (the config path plus the command's own) and opens the store.
func setup(fs *flag.FlagSet, args []string) (*env, error) {
	def := os.Getenv("EVALBENCH_CONFIG")
	if def == "" {
		def = "bench.yaml"
	}
	path := fs.String("config", def, "config file")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return nil, err
	}
	st, err := store.Open(cfg.DB)
	if err != nil {
		return nil, err
	}
	rn := runner.New(st, cfg)
	rn.Log = func(format string, a ...any) { log.Printf(format, a...) }
	return &env{cfg: cfg, st: st, rn: rn}, nil
}

func serve(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", "", "listen address (default from config)")
	e, err := setup(fs, args)
	if err != nil {
		return err
	}
	defer e.st.Close()
	if n, err := e.st.MarkInterrupted(ctx, 3*runner.HeartbeatEvery); err != nil {
		return err
	} else if n > 0 {
		log.Printf("%d run(s) were left unfinished by an earlier process and are marked interrupted; resume them from the UI", n)
	}
	if *addr == "" {
		*addr = e.cfg.Addr
	}
	srv := &http.Server{Addr: *addr, Handler: server.New(ctx, e.st, e.rn, e.cfg, log.Default()).Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	log.Printf("listening on http://%s", *addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func importCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	name := fs.String("name", "", "dataset name (default: the file name)")
	desc := fs.String("description", "", "description")
	// Flags may come before or after the file name.
	var file string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		file, args = args[0], args[1:]
	}
	e, err := setup(fs, args)
	if err != nil {
		return err
	}
	defer e.st.Close()
	if file == "" {
		file = fs.Arg(0)
	}
	if file == "" {
		return errors.New("import needs a file")
	}
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	items, errs := dataset.Parse(f, dataset.FormatFor(file))
	if len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintln(os.Stderr, " ", e)
		}
		return fmt.Errorf("%d problem(s) in %s; nothing was imported", len(errs), file)
	}
	if *name == "" {
		base := file[strings.LastIndex(file, "/")+1:]
		*name = strings.TrimSuffix(strings.TrimSuffix(base, ".jsonl"), ".csv")
	}
	d, err := e.st.CreateDataset(ctx, *name, *desc, items)
	if err != nil {
		return err
	}
	fmt.Printf("imported %d items as dataset %q\n", d.ItemCount, d.ID)
	return nil
}

func datasets(ctx context.Context, args []string) error {
	e, err := setup(flag.NewFlagSet("datasets", flag.ExitOnError), args)
	if err != nil {
		return err
	}
	defer e.st.Close()
	ds, err := e.st.Datasets(ctx)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tITEMS\tIMPORTED\tSHA256")
	for _, d := range ds {
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\n", d.ID, d.ItemCount, d.CreatedAt.Format("2006-01-02 15:04"), d.SHA256[:12])
	}
	return tw.Flush()
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	ds := fs.String("dataset", "", "dataset id")
	models := fs.String("models", "", "comma-separated model names")
	judgeName := fs.String("judge", "", "judge model (default from config)")
	samples := fs.Int("samples", 0, "judge verdicts per answer (default from config)")
	name := fs.String("name", "", "run name")
	out := fs.String("out", "", "also write the Markdown report here")
	e, err := setup(fs, args)
	if err != nil {
		return err
	}
	defer e.st.Close()
	r, err := e.rn.Create(ctx, *name, *ds, splitList(*models), *judgeName, *samples)
	if err != nil {
		return err
	}
	log.Printf("run %s: %s", r.ID, r.Name)
	return finish(ctx, e, r.ID, *out, e.rn.Execute(ctx, r.ID))
}

func finish(ctx context.Context, e *env, runID, out string, runErr error) error {
	if runErr != nil {
		if errors.Is(runErr, context.Canceled) {
			fmt.Fprintf(os.Stderr, "cancelled; resume with: evalbench resume %s\n", runID)
		}
		return runErr
	}
	rep, err := report.Build(context.WithoutCancel(ctx), e.st, runID)
	if err != nil {
		return err
	}
	md := rep.Markdown()
	fmt.Print(md)
	if out != "" {
		return os.WriteFile(out, []byte(md), 0o644)
	}
	return nil
}

func resume(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("resume", flag.ExitOnError)
	out := fs.String("out", "", "also write the Markdown report here")
	id, rest := firstArg(args)
	e, err := setup(fs, rest)
	if err != nil {
		return err
	}
	defer e.st.Close()
	if id == "" {
		return errors.New("resume needs a run id")
	}
	return finish(ctx, e, id, *out, e.rn.Execute(ctx, id))
}

func judge(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("judge", flag.ExitOnError)
	judgeName := fs.String("judge", "", "second judge model")
	samples := fs.Int("samples", 1, "verdicts per answer")
	out := fs.String("out", "", "also write the Markdown report here")
	id, rest := firstArg(args)
	e, err := setup(fs, rest)
	if err != nil {
		return err
	}
	defer e.st.Close()
	r, err := e.st.Run(ctx, id)
	if err != nil {
		return fmt.Errorf("run %q: %w", id, err)
	}
	if *judgeName == "" || *judgeName == r.Judge {
		return errors.New("-judge must name a different judge than the run's primary one")
	}
	return finish(ctx, e, id, *out, e.rn.Judge(ctx, id, *judgeName, *samples, false))
}

func rescore(ctx context.Context, args []string) error {
	id, rest := firstArg(args)
	e, err := setup(flag.NewFlagSet("rescore", flag.ExitOnError), rest)
	if err != nil {
		return err
	}
	defer e.st.Close()
	if err := e.rn.Rescore(ctx, id); err != nil {
		return fmt.Errorf("run %q: %w", id, err)
	}
	fmt.Fprintf(os.Stderr, "rescored %s\n", id)
	return nil
}

func reportCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	format := fs.String("format", "md", "md, csv (one row per model) or results (one row per answer)")
	out := fs.String("out", "", "write to this file instead of stdout")
	id, rest := firstArg(args)
	e, err := setup(fs, rest)
	if err != nil {
		return err
	}
	defer e.st.Close()
	rep, err := report.Build(ctx, e.st, id)
	if err != nil {
		return fmt.Errorf("run %q: %w", id, err)
	}
	var b []byte
	switch *format {
	case "md":
		b = []byte(rep.Markdown())
	case "csv":
		b = rep.SummaryCSV()
	case "results":
		res, err := e.st.Results(ctx, id, store.Filter{})
		if err != nil {
			return err
		}
		b = report.ResultsCSV(res)
	default:
		return fmt.Errorf("unknown format %q", *format)
	}
	if *out != "" {
		return os.WriteFile(*out, b, 0o644)
	}
	_, err = os.Stdout.Write(b)
	return err
}

func runs(ctx context.Context, args []string) error {
	e, err := setup(flag.NewFlagSet("runs", flag.ExitOnError), args)
	if err != nil {
		return err
	}
	defer e.st.Close()
	rs, err := e.st.Runs(ctx)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATUS\tDATASET\tMODELS\tJUDGE\tNAME")
	for _, r := range rs {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", r.ID, r.Status, r.DatasetID, strings.Join(r.Models, ","), r.Judge, r.Name)
	}
	return tw.Flush()
}

func firstArg(args []string) (string, []string) {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return args[0], args[1:]
	}
	return "", args
}
