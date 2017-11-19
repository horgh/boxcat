package boxcat

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
	recvChan1, sendChan1, _, err := client1.start()
	if err != nil {
		catbox.stop()
		t.Logf("error starting client: %s", err)
		t.FailNow()
	}

	client2 := newClient("client2", "127.0.0.1", catbox.Port)
	recvChan2, _, _, err := client2.start()
	if err != nil {
		t.Logf("error starting client: %s", err)
		catbox.stop()
		client1.stop()
		t.FailNow()
	}

	if !waitForMessage(t, recvChan1, irc.Message{Command: irc.ReplyWelcome},
		"welcome from %s", client1.Nick) {
		catbox.stop()
		client1.stop()
		client2.stop()
		t.FailNow()
	}
	if !waitForMessage(t, recvChan2, irc.Message{Command: irc.ReplyWelcome},
		"welcome from %s", client2.Nick) {
		catbox.stop()
		client1.stop()
		client2.stop()
		t.FailNow()
	}

	sendChan1 <- irc.Message{
		Command: "PRIVMSG",
		Params:  []string{client2.Nick, "hi there"},
	}

	if !waitForMessage(t, recvChan2, irc.Message{
		Command: "PRIVMSG",
		Params:  []string{client2.Nick, "hi there"},
	},
		"%s received PRIVMSG from %s", client1.Nick, client2.Nick) {
		catbox.stop()
		client1.stop()
		client2.stop()
		t.FailNow()
	}

	catbox.stop()
	client1.stop()
	client2.stop()
}

func waitForMessage(
	t *testing.T,
	ch <-chan irc.Message,
	want irc.Message,
	format string,
	a ...interface{},
) bool {
	for {
		select {
		case <-time.After(10 * time.Second):
			t.Logf("timeout waiting for message: %s", want)
			return false
		case got := <-ch:
			if got.Command == want.Command {
				log.Printf("got command: %s", fmt.Sprintf(format, a...))
				return true
			}
		}
	}
}
