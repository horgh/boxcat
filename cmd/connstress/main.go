// This program is to test behaviour when clients connect and try to use the
// same nickname at the "same time".
//
// I'm writing this because I suspect there is a bug in catbox related to that
// as I saw a client connect to 2 servers at once, apparently try the same
// nick, and then one server exited. Unfortunately I have no logs to see
// exactly what happened.
//
// What this program will do is repeatedly try to connect to two different
// servers that are linked together, and try to use the same nick. One will
// fall back to an alternate one. Repeat until an issue occurs (or doesn't).
package main

import (
	"bufio"
	"fmt"
	"log"
	"math/rand"
	"net"
	"sync"
	"time"

	"github.com/horgh/irc"
)

func main() {
	var wg sync.WaitGroup

	ports := []int{6667, 6668}
	countPerPort := 50
	for _, port := range ports {
		for i := 0; i < countPerPort; i++ {
			wg.Add(1)
			go func(port int) {
				for {
					if err := client("a", "127.0.0.1", port); err != nil {
						// If you see this a lot then you may need to bump the
						// MaxAllowedPreRegisterMessageCount value.
						log.Printf("client failed: %s", err)
					}
				}
			}(port)
		}
	}

	wg.Wait()
}

func client(nick, host string, port int) error {
	conn, rw, err := connect(host, port)
	if err != nil {
		return fmt.Errorf("error connecting: %s", err)
	}

	if err := writeMessage(conn, rw, irc.Message{
		Command: "NICK",
		Params:  []string{nick},
	}); err != nil {
		_ = conn.Close()
		return fmt.Errorf("error writing NICK: %s", err)
	}

	if err := writeMessage(conn, rw, irc.Message{
		Command: "USER",
		Params:  []string{nick, nick, "0", nick},
	}); err != nil {
		_ = conn.Close()
		return fmt.Errorf("error writing USER: %s", err)
	}

	for {
		m, err := readMessage(conn, rw)
		if err != nil {
			_ = conn.Close()
			return fmt.Errorf("error reading: %s", err)
		}

		if m.Command == "001" {
			// Stick around for a moment to give other connection a chance to
			// collide.
			ms := rand.Int() % 1000
			sleepTime := time.Duration(ms) * time.Millisecond
			log.Printf("welcomed with nick %s, waiting %s", nick, sleepTime)
			time.Sleep(sleepTime)
			if err := writeMessage(conn, rw, irc.Message{
				Command: "QUIT",
				Params:  []string{"bye"},
			}); err != nil {
				_ = conn.Close()
				return fmt.Errorf("error sending QUIT: %s", err)
			}
			break
		}

		if m.Command == "433" {
			log.Printf("nick %s in use", nick)
			nick2, err := newNick(nick)
			if err != nil {
				return err
			}
			nick = nick2
			if err := writeMessage(conn, rw, irc.Message{
				Command: "NICK",
				Params:  []string{nick},
			}); err != nil {
				_ = conn.Close()
				return fmt.Errorf("error writing NICK: %s", err)
			}
			continue
		}

		// A command I hacked into catbox while testing to ensure we hit the case
		// where we see nick in use during registerUser().
		if m.Command == "444" {
			log.Printf("got 444 command")
			continue
		}

		if m.Command == "ERROR" {
			log.Printf("Got error: %s", m)
			continue
		}
	}

	return nil
}

func connect(host string, port int) (net.Conn, *bufio.ReadWriter, error) {
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}

	conn, err := dialer.Dial("tcp", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		return nil, nil, fmt.Errorf("error dialing: %s", err)
	}

	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))

	return conn, rw, nil
}

var writeTimeout = 2 * time.Second

func writeMessage(conn net.Conn, rw *bufio.ReadWriter, m irc.Message) error {
	buf, err := m.Encode()
	if err != nil && err != irc.ErrTruncated {
		return fmt.Errorf("unable to encode message: %s", err)
	}

	if err := conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		return fmt.Errorf("unable to set deadline: %s", err)
	}

	sz, err := rw.WriteString(buf)
	if err != nil {
		return err
	}

	if sz != len(buf) {
		return fmt.Errorf("short write")
	}

	if err := rw.Flush(); err != nil {
		return fmt.Errorf("flush error: %s", err)
	}

	return nil
}

var readTimeout = 2 * time.Second

func readMessage(conn net.Conn, rw *bufio.ReadWriter) (irc.Message, error) {
	if err := conn.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
		return irc.Message{}, fmt.Errorf("unable to set deadline: %s", err)
	}

	line, err := rw.ReadString('\n')
	if err != nil {
		return irc.Message{}, err
	}

	m, err := irc.ParseMessage(line)
	if err != nil && err != irc.ErrTruncated {
		return irc.Message{}, fmt.Errorf("unable to parse message: %s: %s", line,
			err)
	}

	return m, nil
}

const maxNickLen = 15

func newNick(oldNick string) (string, error) {
	if len(oldNick) < maxNickLen {
		return oldNick + "a", nil
	}

	for i := 0; i < maxNickLen; i++ {
		if oldNick[i] < 'z' {
			newNick := oldNick[0:i] + string(oldNick[i]+1)
			if i < maxNickLen-1 {
				newNick += oldNick[i+1:]
			}
			return newNick, nil
		}
	}

	return "", fmt.Errorf("exhausted nicks")
}
