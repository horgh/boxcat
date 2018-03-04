// This program is to help test issues around showing the QUIT message in
// catbox. What we see instead is the generic "I/O error" quit message. I think
// this is because there's a race between processing the QUIT command from the
// client vs. the socket being closed.
package main

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"time"

	"github.com/horgh/irc"
)

func main() {
	if err := client("a", "199.167.128.16", 6667); err != nil {
		log.Printf("client failed: %s", err)
	}
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

	if err := writeMessage(conn, rw, irc.Message{
		Command: "JOIN",
		Params:  []string{"#test"},
	}); err != nil {
		_ = conn.Close()
		return fmt.Errorf("error writing JOIN: %s", err)
	}

	if err := writeMessage(conn, rw, irc.Message{
		Command: "QUIT",
		Params:  []string{"bye bye"},
	}); err != nil {
		_ = conn.Close()
		return fmt.Errorf("error writing QUIT: %s", err)
	}

	if err := conn.Close(); err != nil {
		return fmt.Errorf("error closing connection: %s", err)
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
