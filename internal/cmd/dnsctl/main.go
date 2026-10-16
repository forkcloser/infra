// dnsctl makes deSEC and the registrar match the zones file.
//
// plan reads both and prints every change; apply makes them. Tokens come
// from the environment: DESEC_API_TOKEN and GANDI_PERSONAL_ACCESS_TOKEN.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/alecthomas/kong"
	"github.com/mycophonic/primordium/app"
	"github.com/mycophonic/primordium/app/shutdown"
	"github.com/mycophonic/primordium/network/transporter"

	"github.com/forkcloser/infra/internal/desec"
	"github.com/forkcloser/infra/internal/gandi"
	"github.com/forkcloser/infra/internal/reconcile"
	"github.com/forkcloser/infra/internal/zones"
)

// Exit statuses: plan says whether anything would change the way
// `tofu plan -detailed-exitcode` does, so a workflow can gate on it.
const (
	exitOK      = 0
	exitError   = 1
	exitChanges = 2
)

// deSEC allows two record-set writes per second per zone, and both
// services answer 429 with Retry-After, which the transport honours.
const (
	requestsPerSecond = 2
	maxRetries        = 5
	initialBackoff    = time.Second
	maxBackoff        = time.Minute
)

type rootCommand struct {
	DesecToken       string `env:"DESEC_API_TOKEN"                      help:"deSEC token, restricted to the zones in the file."                                                     required:"true"`
	DesecCreateToken string `env:"DESEC_CREATE_TOKEN"                   help:"deSEC token whose only right is to create domains; needed when a zone in the file does not exist yet."`
	GandiToken       string `env:"GANDI_PERSONAL_ACCESS_TOKEN"          help:"Gandi personal access token with domain management."                                                   required:"true"`
	DesecBaseURL     string `default:"https://desec.io/api/v1/"         help:"deSEC API base."                                                                                       hidden:""`
	GandiBaseURL     string `default:"https://api.gandi.net/v5/domain/" help:"Gandi domain API base."                                                                                hidden:""`

	Plan  planCommand  `cmd:"" help:"Show what apply would change. Exit 2 when something would."`
	Apply applyCommand `cmd:"" help:"Make deSEC and the registrar match the zones file."`

	// Where the plan is written; unexported, so kong leaves it alone.
	out io.Writer
}

type planCommand struct {
	Zones string `arg:"" help:"The zones file." type:"existingfile"`
}

type applyCommand struct {
	Zones string `arg:"" help:"The zones file." type:"existingfile"`
}

var errChanges = errors.New("changes pending")

// transportOptions is one client's rate limit and retry policy; each
// service gets a client of its own so its TLS can differ.
func transportOptions(legacyTLS bool) transporter.Options {
	return transporter.Options{
		MaxPerSecond:   requestsPerSecond,
		MaxRetries:     maxRetries,
		InitialBackoff: initialBackoff,
		MaxBackoff:     maxBackoff,
		UserAgent:      "dnsctl",
		LegacyTLS:      legacyTLS,
	}
}

func (root *rootCommand) reconciler() (*reconcile.Reconciler, error) {
	// deSEC speaks TLS 1.3 and takes the process default: 1.3, hybrid
	// post-quantum key exchange only.
	desecClient, err := desec.New(
		transporter.NewClient(transportOptions(false)),
		root.DesecBaseURL, root.DesecToken, root.DesecCreateToken,
	)
	if err != nil {
		return nil, fmt.Errorf("deSEC client: %w", err)
	}

	// api.gandi.net refuses a TLS 1.3 handshake ("protocol version not
	// supported", the server's alert, on every attempt) and answers at
	// 1.2, so this one client gets Go's default TLS. Nothing else does.
	gandiClient, err := gandi.New(
		transporter.NewClient(transportOptions(true)),
		root.GandiBaseURL, root.GandiToken,
	)
	if err != nil {
		return nil, fmt.Errorf("Gandi client: %w", err)
	}

	return &reconcile.Reconciler{DeSEC: desecClient, Gandi: gandiClient}, nil
}

// plan loads the file and computes the changes; both commands start here.
func (root *rootCommand) plan(ctx context.Context, path string) (*reconcile.Reconciler, *reconcile.Plan, error) {
	cfg, err := zones.Load(path)
	if err != nil {
		return nil, nil, fmt.Errorf("zones file: %w", err)
	}

	rec, err := root.reconciler()
	if err != nil {
		return nil, nil, err
	}

	plan, err := rec.Plan(ctx, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("planning: %w", err)
	}

	plan.Write(root.out)

	return rec, plan, nil
}

func (cmd *planCommand) Run(ctx context.Context, root *rootCommand) error {
	_, plan, err := root.plan(ctx, cmd.Zones)
	if err != nil {
		return err
	}

	if !plan.Empty() {
		return errChanges
	}

	return nil
}

func (cmd *applyCommand) Run(ctx context.Context, root *rootCommand) error {
	rec, plan, err := root.plan(ctx, cmd.Zones)
	if err != nil {
		return err
	}

	if plan.Empty() {
		return nil
	}

	if err := rec.Apply(ctx, plan); err != nil {
		return fmt.Errorf("applying: %w", err)
	}

	return nil
}

// run parses args and runs the command, writing the plan to stdout and
// usage to stderr, and returns the exit status. The context is bound to
// the interface type the Run methods take; kong binds by concrete type
// otherwise, and the one app.Main hands out is not context.Context.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	root := rootCommand{out: stdout}

	parser, err := kong.New(&root,
		kong.Name("dnsctl"),
		kong.Description("Make deSEC and the registrar match the zones file."),
		kong.UsageOnError(),
		kong.Writers(stdout, stderr),
		kong.Exit(func(int) {}),
		kong.BindTo(ctx, (*context.Context)(nil)),
	)
	if err != nil {
		fmt.Fprintln(stderr, "dnsctl:", err)

		return exitError
	}

	kctx, err := parser.Parse(args)
	if err != nil {
		parser.Errorf("%s", err)

		return exitError
	}

	err = kctx.Run(&root)

	switch {
	case err == nil:
		return exitOK
	case errors.Is(err, errChanges):
		return exitChanges
	default:
		fmt.Fprintln(stderr, "dnsctl:", err)

		return exitError
	}
}

func main() {
	// Main runs the handlers it registered (the reporter's flush, the stores)
	// however run ends, and exits with the status an ExitError asks for,
	// silently: the changes status is 2, and every message is already printed.
	app.Main(&app.Options{Name: "dnsctl"}, func(ctx context.Context) error {
		if code := run(ctx, os.Args[1:], os.Stdout, os.Stderr); code != exitOK {
			return shutdown.ExitError(code)
		}

		return nil
	})
}
