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
	defer catbox.stop()

	client1 := newClient("client1", "127.0.0.1", catbox.Port)
	recvChan1, sendChan1, _, err := client1.start()
	if err != nil {
		t.Fatalf("error starting client: %s", err)
	}
	defer client1.stop()

	client2 := newClient("client2", "127.0.0.1", catbox.Port)
	recvChan2, _, _, err := client2.start()
	if err != nil {
		t.Fatalf("error starting client: %s", err)
	}
	defer client2.stop()

	if !waitForMessage(t, recvChan1, irc.Message{Command: irc.ReplyWelcome},
		"welcome from %s", client1.Nick) {
		t.Fatalf("client1 did not get welcome")
	}
	if !waitForMessage(t, recvChan2, irc.Message{Command: irc.ReplyWelcome},
		"welcome from %s", client2.Nick) {
		t.Fatalf("client2 did not get welcome")
	}

	sendChan1 <- irc.Message{
		Command: "PRIVMSG",
		Params:  []string{client2.Nick, "hi there"},
	}

	if !waitForMessage(
		t,
		recvChan2,
		irc.Message{
			Command: "PRIVMSG",
			Params:  []string{client2.Nick, "hi there"},
		},
		"%s received PRIVMSG from %s", client1.Nick, client2.Nick,
	) {
		t.Fatalf("client1 did not receive message from client2")
	}
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
