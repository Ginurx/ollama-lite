// Command ollama-lite starts a lightweight, Ollama-compatible server that runs
// cloud models by signing requests to ollama.com with the shared ~/.ollama key.
// It deliberately omits all local model hosting (no llama.cpp, GPU, or model
// storage), so the binary is tiny and needs no large install.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"time"

	"ollama-lite/internal/auth"
	"ollama-lite/internal/config"
	"ollama-lite/internal/launch"
	"ollama-lite/internal/server"
	"ollama-lite/internal/tui"
)

func main() {
	log.SetFlags(0)

	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	var err error
	switch os.Args[1] {
	case "serve":
		err = serveCmd(os.Args[2:])
	case "launch":
		err = launchCmd(os.Args[2:])
	case "signin":
		err = signinCmd(os.Args[2:])
	case "signout":
		err = signoutCmd(os.Args[2:])
	case "whoami":
		err = whoamiCmd(os.Args[2:])
	case "version", "-v", "--version":
		fmt.Printf("ollama-lite %s\n", server.Version)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(1)
	}

	if err != nil {
		log.Fatalf("error: %v", err)
	}
}

func usage() {
	fmt.Print(`ollama-lite - a lightweight, cloud-only Ollama server

Usage:
    ollama-lite <command> [flags]

Commands:
    serve      Start the Ollama-compatible server (proxies cloud models)
    launch     Launch an AI app wired to use ollama-lite as its backend
    signin     Connect this machine to your ollama.com account
    signout    Disconnect this machine from ollama.com
    whoami     Show the signed-in ollama.com account
    version    Print the ollama-lite version

Environment (shared with the official Ollama):
    OLLAMA_HOST              Address to listen on (default 127.0.0.1:11434)
    OLLAMA_ORIGINS           Extra allowed CORS origins (comma-separated)
    OLLAMA_CLOUD_BASE_URL    Cloud endpoint (default https://ollama.com)

Run "ollama-lite serve --help" for serve flags.
Run "ollama-lite launch --help" for the list of supported apps.
`)
}

func serveCmd(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	models := fs.String("models", "", "comma-separated models to advertise on /api/tags (overrides config)")
	host := fs.String("host", "", "address to listen on, e.g. 127.0.0.1:11435 or :11434 (overrides OLLAMA_HOST)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if err := auth.EnsureKey(); err != nil {
		return fmt.Errorf("preparing signing key: %w", err)
	}

	addr := config.BindAddressFrom(*host)
	list := config.Models(*models)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	log.Printf("ollama-lite %s listening on http://%s (cloud: %s)", server.Version, addr, config.CloudBaseURL())
	if len(list) > 0 {
		log.Printf("advertising %d model(s) on /api/tags: %s", len(list), strings.Join(list, ", "))
	} else {
		log.Printf("advertising recommended cloud models on /api/tags (online, from %s/api/experimental/model-recommendations)", config.CloudBaseURL())
	}

	return server.Serve(ctx, addr, list)
}

// launchCmd starts an AI app configured to use the ollama-lite server as its
// backend. Usage: ollama-lite launch <app> [--model M] [--host H] [-- extra...]
func launchCmd(args []string) error {
	// Everything after the first "--" is passed through to the app unchanged.
	var extra []string
	for i, a := range args {
		if a == "--" {
			extra = append([]string{}, args[i+1:]...)
			args = args[:i]
			break
		}
	}

	var model, hostOverride, name string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--model" || a == "-model":
			if i+1 >= len(args) {
				return fmt.Errorf("--model requires a value")
			}
			model, i = args[i+1], i+1
		case strings.HasPrefix(a, "--model="):
			model = strings.TrimPrefix(a, "--model=")
		case a == "--host" || a == "-host":
			if i+1 >= len(args) {
				return fmt.Errorf("--host requires a value")
			}
			hostOverride, i = args[i+1], i+1
		case strings.HasPrefix(a, "--host="):
			hostOverride = strings.TrimPrefix(a, "--host=")
		case a == "-h" || a == "--help" || a == "help":
			launchUsage()
			return nil
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("unknown flag %q for launch", a)
		default:
			if name != "" {
				return fmt.Errorf("unexpected argument %q; use '--' to pass extra args to the app", a)
			}
			name = a
		}
	}

	if name == "" {
		launchUsage()
		return nil
	}

	canonical, ok := launch.Resolve(name)
	if !ok {
		return fmt.Errorf("unknown app %q\n\nSupported apps: %s", name, strings.Join(launch.Supported(), ", "))
	}

	host := config.ConnectableHostFrom(hostOverride)

	// Resolve the model. With an explicit --model we use it as-is. Otherwise,
	// on an interactive terminal, fetch the advertised models from the running
	// ollama-lite server's /api/tags and show a picker with the saved default
	// (~/.ollama-lite/config.json) pre-selected. When not a terminal, fall back
	// to the saved default, then the first server-advertised model.
	if model = strings.TrimSpace(model); model == "" {
		def := config.LaunchDefaultModel(canonical)
		if tui.Interactive() {
			models, err := launch.FetchModelsFromServer(host)
			if err != nil {
				return fmt.Errorf("couldn't reach ollama-lite server at %s: %w\nstart it with 'ollama-lite serve', or pass --model explicitly", host, err)
			}
			if len(models) == 0 {
				return fmt.Errorf("ollama-lite server at %s advertises no models; configure ~/.ollama-lite/models.json (or pass --models to serve), or pass --model explicitly", host)
			}
			chosen, err := tui.SelectModel(fmt.Sprintf("Select a model for %s:", canonical), models, def)
			switch {
			case err == nil:
				model = chosen
			case errors.Is(err, tui.ErrCanceled):
				fmt.Fprintln(os.Stderr, "launch canceled")
				return nil
			default:
				fmt.Fprintf(os.Stderr, "Warning: model picker unavailable: %v\n", err)
			}
		}
		if model == "" {
			model = def
		}
		if model == "" {
			if models, err := launch.FetchModelsFromServer(host); err == nil && len(models) > 0 {
				model = models[0]
			}
		}
	}
	if model == "" {
		return fmt.Errorf("no model specified; pass --model <model> (e.g. --model gpt-oss:120b)")
	}

	// Remember this model as the default for next time. Best-effort: never block
	// the launch if the config can't be written.
	if err := config.SaveLaunchModel(canonical, model); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not save default model to ~/.ollama-lite/config.json: %v\n", err)
	}

	warnIfServerUnreachable(host)

	log.Printf("launching %s with model %q via %s", canonical, model, host.String())
	return launch.Launch(canonical, model, extra, host)
}

func launchUsage() {
	fmt.Printf(`ollama-lite launch - start an AI app wired to ollama-lite

Usage:
    ollama-lite launch <app> [--model MODEL] [--host HOST] [-- EXTRA_ARGS...]

Flags:
    --model MODEL   Model to use. Omit it on a terminal to pick interactively
                    from the advertised models (the saved default is
                    pre-selected; Enter reuses it). Non-interactive shells fall
                    back to the saved default, then the first advertised model.
                    Passing --model records it as this app's default in
                    ~/.ollama-lite/config.json.
    --host HOST     ollama-lite address the app should connect to
                    (overrides OLLAMA_HOST; e.g. 127.0.0.1:11435)

Supported apps:
%s

Examples:
    ollama-lite launch claude --model gpt-oss:120b   # sets & remembers the model
    ollama-lite launch claude                        # pick from a list (Enter reuses saved)
    ollama-lite launch codex -- --sandbox workspace-write

Note: start the server first with "ollama-lite serve".
`, "    "+strings.Join(launch.SupportedList(), "\n    "))
}

// warnIfServerUnreachable prints a hint (but does not fail) when nothing answers
// at the ollama-lite address the launched app is about to use.
func warnIfServerUnreachable(host *url.URL) {
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := client.Get(strings.TrimRight(host.String(), "/") + "/api/version")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: no ollama-lite server reachable at %s; start it with 'ollama-lite serve'\n", host.String())
		return
	}
	_ = resp.Body.Close()
}

func signinCmd(args []string) error {
	if err := auth.EnsureKey(); err != nil {
		return fmt.Errorf("preparing signing key: %w", err)
	}

	ctx := context.Background()
	if user, err := fetchWhoami(ctx); err == nil && user.Name != "" {
		fmt.Printf("You are already signed in as '%s'.\n", user.Name)
		return nil
	}

	signinURL, err := auth.SigninURL()
	if err != nil {
		return err
	}

	fmt.Println("To run Ollama cloud models, connect this machine to your ollama.com account:")
	fmt.Println()
	fmt.Printf("    %s\n", signinURL)
	fmt.Println()
	fmt.Println("Opening your browser... (if it does not open, use the link above)")
	openBrowser(signinURL)
	return nil
}

func signoutCmd(args []string) error {
	encKey, err := auth.EncodedPublicKey()
	if err != nil {
		return err
	}

	resp, err := server.SignedRequest(context.Background(), http.MethodDelete, "/api/user/keys/"+encKey, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		fmt.Println("You are not signed in to ollama.com.")
	case resp.StatusCode >= http.StatusBadRequest:
		return fmt.Errorf("signout failed: %s", statusBody(resp))
	default:
		fmt.Println("You have signed out of ollama.com.")
	}
	return nil
}

func whoamiCmd(args []string) error {
	user, err := fetchWhoami(context.Background())
	if err != nil {
		if err == errUnauthorized {
			fmt.Println("You are not signed in. Run 'ollama-lite signin'.")
			return nil
		}
		return err
	}

	fmt.Printf("Signed in as: %s\n", user.Name)
	if user.Email != "" {
		fmt.Printf("Email:        %s\n", user.Email)
	}
	if user.Plan != "" {
		fmt.Printf("Plan:         %s\n", user.Plan)
	}
	return nil
}

type userResponse struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Plan  string `json:"plan"`
}

var errUnauthorized = fmt.Errorf("unauthorized")

func fetchWhoami(ctx context.Context) (userResponse, error) {
	var user userResponse

	resp, err := server.SignedRequest(ctx, http.MethodPost, "/api/me", nil)
	if err != nil {
		return user, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return user, errUnauthorized
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return user, fmt.Errorf("ollama.com returned %s", statusBody(resp))
	}

	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return user, err
	}
	if user.Name == "" {
		return user, errUnauthorized
	}
	return user, nil
}

func statusBody(resp *http.Response) string {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		return resp.Status
	}
	return fmt.Sprintf("%s: %s", resp.Status, msg)
}

func openBrowser(u string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	case "darwin":
		cmd = exec.Command("open", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	_ = cmd.Start()
}
