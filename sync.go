// Package imapsync provides a single function to download an IMAP folder to a local directory, with each email
// in a plain text file. Emails are downloaded only once, even if the function is run repeatedly.
//
// A command line tool is available at https://github.com/JohannesEbke/go-imap-sync/cmd/go-imap-sync
package main

import (
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"log/slog"
	"mime"
	"os"
	"path/filepath"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	client "github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-message/charset"
)

// Result contains slices of (relative) paths to the newly (NewEmails) and previously downloaded emails (ExistingEmails).
// Only emails still present on the server will be returned.
type Result struct {
	ExistingEmails []string
	NewEmails      []string
}

// connect performs an interactive connection to the given IMAP server
func connect(server, username, password string) (*client.Client, error) {
	options := &imapclient.Options{
		WordDecoder: &mime.WordDecoder{CharsetReader: charset.Reader},
	}
	slog.Debug("Connecting to server", "server", server, "user", username)
	c, err := client.DialTLS(server, options)
	if err != nil {
		return nil, fmt.Errorf("error connecting to %v: %v", server, err)
	}
	slog.Debug("connected to server, begin login", "server", server, "user", username)

	err = c.WaitGreeting()
	if err != nil {
		return nil, fmt.Errorf("error waiting for greeting from %v: %v", server, err)
	}
	slog.Debug("greeting received")

	if err := c.Login(username, password).Wait(); err != nil {
		if err2 := c.Logout().Wait(); err2 != nil {
			return nil, fmt.Errorf("error while logging in to %v: %v\n(logout error: %v)", server, err, err2)
		}
		return nil, fmt.Errorf("error while logging in to %v: %v", server, err)
	}
	slog.Debug("Logged in as user", "user", username, "server", server)
	return c, nil
}

// Sync downloads and saves all not-yet downloaded emails from the mailbox to the emailDir
func Sync(server, user, password, mailbox, emailDir string) (*Result, error) {
	err := os.MkdirAll(emailDir, 0o700)
	if err != nil {
		return nil, fmt.Errorf("error creating email directory %v: %v", emailDir, err)
	}

	slog.Debug("Connecting to server", "server", server, "user", user)
	connection, err := connect(server, user, password)
	if err != nil {
		return nil, err
	}

	defer func() {
		err2 := connection.Logout().Wait()
		if err2 != nil {
			slog.Error("error on logout from server", "server", server, "user", user, "error", err2)
		}
	}()

	selectCmd := connection.Select(mailbox, &imap.SelectOptions{})

	selectData, err := selectCmd.Wait()
	if err != nil {
		return nil, fmt.Errorf("error selecting mailbox %v: %v", mailbox, err)
	}
	slog.Debug("selected mailbox", "mailbox", mailbox, "numMessages", selectData.NumMessages, "selectData", selectData)

	// Send a FETCH command to fetch the message body
	seqSet := imap.SeqSetNum(1)
	fetchOptions := &imap.FetchOptions{
		UID:         true,
		Envelope:    true,
		BodySection: []*imap.FetchItemBodySection{{}},
	}
	fetchCmd := connection.Fetch(seqSet, fetchOptions)
	defer fetchCmd.Close()

	var result Result
	// a map of sequence numbers to email MessageID
	seqNumMessageIDMap := make(map[uint32]string)
	// see https://pkg.go.dev/github.com/emersion/go-imap/v2/imapclient#example-Client.Fetch-StreamBody

	slog.Debug("listing all messages in mailbox", "mailbox", mailbox)
	for {
		slog.Debug("fetching message")
		msg := fetchCmd.Next()
		if msg == nil {
			slog.Debug("stop fetching due to no more messages")
			break
		}

		slog.Debug("fetched message", "seq", msg.SeqNum)
		// msgBuf, err := msg.Collect()
		// if err != nil {
		// 	log.Fatal(err)
		// }
		// slog.Info("parsed Message", "seq", msgBuf.SeqNum, "uid", msgBuf.Envelope.MessageID)

		// get BodySectionName BODY[]
		// Find the uid, envelope, body section in the response
		var uid uint32
		var envelope *imap.Envelope
		var bodySection imapclient.FetchItemDataBodySection
		for {
			item := msg.Next()
			if item == nil {
				slog.Debug("stop parse due to no more items in message")
				break
			}

			switch item := item.(type) {
			case imapclient.FetchItemDataUID:
				log.Printf("UID: %v", item.UID)
				uid = uint32(item.UID)
			case imapclient.FetchItemDataEnvelope:
				log.Printf("Envelope MessageID: %v", item.Envelope.MessageID)
				envelope = item.Envelope
			case imapclient.FetchItemDataBodySection:
				bodySection = item
			}

			// early return if we have all the data we need
			if uid != 0 && envelope != nil && bodySection.Literal != nil {
				seqNumMessageIDMap[uid] = envelope.MessageID

				slog.Debug("have all data we need", "seq", msg.SeqNum, "uid", uid,
					"messageID", envelope.MessageID, "subject", envelope.Subject)

				exists, err := fileExists(messageFileName(emailDir, envelope.MessageID))
				if err != nil {
					log.Fatal(err)
				}
				if exists {
					result.ExistingEmails = append(result.ExistingEmails, messageFileName(emailDir, envelope.MessageID))
				} else {
					result.NewEmails = append(result.NewEmails, messageFileName(emailDir, envelope.MessageID))
					log.Printf("Writing message %v to %v", envelope.MessageID, messageFileName(emailDir, envelope.MessageID))

					body, err := io.ReadAll(bodySection.Literal)
					if err != nil {
						log.Fatalf("failed to read body section: %v", err)
					}
					slog.Debug("Body", "body", string(body))
					err = os.WriteFile(messageFileName(emailDir, envelope.MessageID), body, 0o600)
					if err != nil {
						log.Fatalf("failed to write body to file: %v", err)
					}
				}
				break
			}
		}
	}

	log.Printf("Finished syncing.")

	return &result, nil
}

// sha512TruncatedHex returns a hex representation of the first 32 bytes of the SHA512 hash of the given string
func sha512TruncatedHex(messageID string) string {
	h := sha512.New()
	if _, err := io.WriteString(h, messageID); err != nil {
		log.Fatal(err)
	}
	b := h.Sum(nil)
	return hex.EncodeToString(b[0:31])
}

// messageFileName returns the target file name of the email with the given messageID
func messageFileName(emailDir, messageID string) string {
	return filepath.Join(emailDir, fmt.Sprintf("%s.eml", sha512TruncatedHex(messageID)))
}

// fileExists checks if the given path exists and can be Stat'd.
func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return true, err
}
