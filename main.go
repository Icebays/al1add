// Command al1add is a self-hosted network toolbox with a web panel:
// web service / reverse proxy, DDNS, port forwarding, STUN traversal, SSL
// certificates, network storage, Docker, Cloudflare management and more.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/icebays/al1add/internal/admin"
	"github.com/icebays/al1add/internal/core"
	"github.com/icebays/al1add/internal/logx"
	"github.com/icebays/al1add/internal/modules"
	"github.com/icebays/al1add/internal/store"
)

func main() {
	dataDir := flag.String("cd", defaultDataDir(), "data (configuration) directory")
	passphrase := flag.String("passphrase", os.Getenv("AL1ADD_PASSPHRASE"), "optional passphrase protecting master.key (env AL1ADD_PASSPHRASE)")
	resetUser := flag.String("resetuser", "", "reset the admin username (use with -resetpass)")
	resetPass := flag.String("resetpass", "", "reset the admin password and exit")
	setPort := flag.Int("setport", 0, "set the panel HTTP port and exit")
	getPort := flag.Bool("getport", false, "print the panel HTTP port and exit")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("al1add", core.Version)
		return
	}

	st, err := store.Open(*dataDir, *passphrase)
	if err != nil {
		log.Fatalf("open data dir %s: %v", *dataDir, err)
	}
	logs := logx.NewHub(500)
	app := core.New(st, logs)

	panel, initial, err := admin.New(app, *passphrase)
	if err != nil {
		log.Fatal(err)
	}
	// One-shot maintenance commands (used by install.sh). If they created the
	// config for the first time, still reveal the generated admin password.
	if *resetPass != "" || *setPort != 0 || *getPort {
		if *resetPass != "" {
			if err := panel.ResetPassword(*resetUser, *resetPass); err != nil {
				log.Fatal(err)
			}
			fmt.Println("admin password updated")
			initial = ""
		}
		if *setPort != 0 {
			if err := panel.SetPort(*setPort); err != nil {
				log.Fatal(err)
			}
			fmt.Println("panel port set to", *setPort)
		}
		if *getPort {
			fmt.Println(panel.Port())
		}
		if initial != "" {
			fmt.Fprintf(os.Stderr, "initial admin user = admin, password = %s\n", initial)
		}
		return
	}

	modules.RegisterAll(app)
	app.LoadAll()
	app.StartAll()

	if err := panel.Start(); err != nil {
		log.Fatalf("start panel: %v", err)
	}
	log.Printf("al1add %s started, data dir: %s", core.Version, *dataDir)
	if initial != "" {
		log.Printf("================================================================")
		log.Printf(" First start: admin user = admin, password = %s", initial)
		log.Printf(" Change it in 系統設定 after logging in.")
		log.Printf("================================================================")
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	log.Printf("shutting down")
	panel.Stop()
	app.StopAll()
}

func defaultDataDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "data"
	}
	return filepath.Join(filepath.Dir(exe), "data")
}
