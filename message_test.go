package main

import (
	"fmt"
	"log"
	"testing"
	"time"

	"github.com/horgh/irc"
)

// Test one client sending a message to another client.
func TestPRIVMSG(t *testing.T) {
	catbox, err := harnessCatbox()
	if err != nil {
		t.Fatalf("error harnessing catbox: %s", err)
	}

	client1 := newClient("client1", "127.0.0.1", catbox.Port)
	recvChan1, sendChan1, errChan1, doneChan1, err := client1.start()
	if err != nil {
		catbox.stop()
		t.Logf("error starting client: %s", err)
		t.FailNow()
	}

	client2 := newClient("client2", "127.0.0.1", catbox.Port)
	recvChan2, sendChan2, errChan2, doneChan2, err := client2.start()
	if err != nil {
		catbox.stop()
		close(sendChan1)
		close(doneChan1)
		t.Logf("error starting client: %s", err)
		t.FailNow()
	}

	waitForMessage(t, recvChan1, irc.Message{Command: irc.ReplyWelcome},
		"welcome from %s", client1.Nick)
	waitForMessage(t, recvChan2, irc.Message{Command: irc.ReplyWelcome},
		"welcome from %s", client2.Nick)

	sendChan1 <- irc.Message{
		Command: "PRIVMSG",
		Params:  []string{client2.Nick, "hi there"},
	}

	close(sendChan1)
	close(sendChan2)

	waitForMessage(t, recvChan2, irc.Message{
		Command: "PRIVMSG",
		Params:  []string{client2.Nick, "hi there"},
	},
		"%s received PRIVMSG from %s", client1.Nick, client2.Nick)

	close(doneChan1)
	close(doneChan2)

	for range recvChan1 {
	}
	for range recvChan2 {
	}

	for err := range errChan1 {
		t.Errorf("client 1 reported error: %s", err)
	}
	for err := range errChan2 {
		t.Errorf("client 2 reported error: %s", err)
	}

	catbox.stop()
}

func waitForMessage(
	t *testing.T,
	ch <-chan irc.Message,
	want irc.Message,
	format string,
	a ...interface{},
) {
	for {
		select {
		case <-time.After(10 * time.Second):
			t.Fatalf("timeout waiting for message: %s", want)
		case got := <-ch:
			if got.Command == want.Command {
				log.Printf("got command: %s", fmt.Sprintf(format, a...))
				return
			}
		}
	}
}
