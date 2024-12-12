// go-imap-sync provides a simple command line tool to download emails from an IMAP mailbox. Each email is saved as a
// plain text file (per default in the messages/ subdirectory). Emails are only downloaded once if run repeatedly.
package main

import (
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/howeyc/gopass"
)

func getPassword(username, server string) (password string) {
	password = os.Getenv("IMAP_PASSWORD")

	if password == "" {
		log.Printf("Enter IMAP Password for %v on %v:", username, server)
		passwordBytes, err := gopass.GetPasswd()
		if err != nil {
			panic(err)
		}
		password = string(passwordBytes)
	}
	return
}

func main() {
	var server, username, mailbox, emailDir string
	flag.StringVar(&server, "server", "", "sync from this mail server and port (e.g. mail.example.com:993)")
	flag.StringVar(&username, "username", "", "username for logging into the mail server")
	flag.StringVar(&mailbox, "mailbox", "", "mailbox to read messages from (typically INBOX or INBOX/subfolder)")
	flag.StringVar(&emailDir, "messagesDir", "messages", "local directory to save messages in")
	flag.Parse()

	if server == "" {
		log.Println("go-imap-sync copies emails from an IMAP mailbox to your computer. Usage:")
		flag.PrintDefaults()
		log.Fatal("Required parameters not found.")
	}

	// set slog text global logger
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level:     slog.LevelDebug,
		AddSource: true,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.SourceKey {
				source := a.Value.Any().(*slog.Source)
				shortFilename := fmt.Sprintf("%s:%d:%s", filepath.Base(source.File), source.Line, source.Function)
				a.Value = slog.StringValue(shortFilename)
				return a
			}
			return a
		},
	})
	slog.SetDefault(slog.New(handler))

	password := getPassword(username, server)

	_, err := Sync(server, username, password, mailbox, emailDir)
	if err != nil {
		log.Fatal(err)
	}
}
