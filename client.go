package main

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/horgh/irc"
)

// Client represents a client connection.
type Client struct {
	Nick       string
	ServerHost string
	ServerPort uint16

	conn        net.Conn
	rw          *bufio.ReadWriter
	timeoutTime time.Duration
}

func newClient(nick, serverHost string, serverPort uint16) *Client {
	return &Client{
		Nick:       nick,
		ServerHost: serverHost,
		ServerPort: serverPort,

		timeoutTime: 5 * time.Minute,
	}
}

// start starts a connection. All messages received from the server will be
// sent on the first channel. Messages sent to the second channel will be sent
// to the server.
//
// The Client takes care of connection registration.
//
// It also responds to PINGs.
//
// If an error occurs, it ends. It does not try to reconnect.
func (c *Client) start() (<-chan irc.Message, chan<- irc.Message, <-chan error,
	error) {
	if err := c.connect(); err != nil {
		return nil, nil, nil, fmt.Errorf("error connecting: %s", err)
	}

	if err := c.nickCommand(c.Nick); err != nil {
		return nil, nil, nil, err
	}
	if err := c.userCommand(c.Nick, c.Nick); err != nil {
		return nil, nil, nil, err
	}

	recvChan := make(chan irc.Message, 512)
	sendChan := make(chan irc.Message, 512)
	errChan := make(chan error, 512)

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			m, err := c.readMessage()
			if err != nil {
				errChan <- fmt.Errorf("error reading message: %s", err)
				close(recvChan)
				return
			}

			if m.Command == "PING" {
				if err := c.pongCommand(m); err != nil {
					errChan <- fmt.Errorf("error sending pong: %s", err)
					close(recvChan)
					return
				}
			}

			recvChan <- m
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for m := range sendChan {
			if err := c.writeMessage(m); err != nil {
				errChan <- fmt.Errorf("error writing message: %s", err)
				return
			}
		}
	}()

	go func() {
		wg.Wait()
		close(errChan)
	}()

	return recvChan, sendChan, errChan, nil
}

// connect opens a new connection to the server.
func (c *Client) connect() error {
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}

	conn, err := dialer.Dial("tcp", fmt.Sprintf("%s:%d", c.ServerHost,
		c.ServerPort))
	if err != nil {
		return fmt.Errorf("error dialing: %s", err)
	}

	c.conn = conn
	c.rw = bufio.NewReadWriter(bufio.NewReader(c.conn), bufio.NewWriter(c.conn))
	return nil
}

// nickCommand sends the NICK command.
func (c *Client) nickCommand(n string) error {
	if err := c.writeMessage(irc.Message{
		Command: "NICK",
		Params:  []string{n},
	}); err != nil {
		return fmt.Errorf("failed to send NICK: %s", err)
	}

	return nil
}

// userCommand sends the USER command.
func (c *Client) userCommand(ident, realName string) error {
	if err := c.writeMessage(irc.Message{
		Command: "USER",
		Params:  []string{ident, "0", "*", realName},
	}); err != nil {
		return fmt.Errorf("failed to send USER: %s", err)
	}

	return nil
}

// pongCommand sends a PONG in response to the given PING message.
func (c *Client) pongCommand(ping irc.Message) error {
	return c.writeMessage(irc.Message{
		Command: "PONG",
		Params:  []string{ping.Params[0]},
	})
}

// writeMessage writes an IRC message to the connection.
func (c Client) writeMessage(m irc.Message) error {
	buf, err := m.Encode()
	if err != nil && err != irc.ErrTruncated {
		return fmt.Errorf("unable to encode message: %s", err)
	}

	return c.write(buf)
}

// write writes a string to the connection
func (c Client) write(s string) error {
	if err := c.conn.SetDeadline(time.Now().Add(c.timeoutTime)); err != nil {
		return fmt.Errorf("unable to set deadline: %s", err)
	}

	sz, err := c.rw.WriteString(s)
	if err != nil {
		return err
	}

	if sz != len(s) {
		return fmt.Errorf("short write")
	}

	if err := c.rw.Flush(); err != nil {
		return fmt.Errorf("flush error: %s", err)
	}

	log.Printf("Sent: %s", strings.TrimRight(s, "\r\n"))

	return nil
}

// readMessage reads a line from the connection and parses it as an IRC message.
func (c Client) readMessage() (irc.Message, error) {
	buf, err := c.read()
	if err != nil {
		return irc.Message{}, err
	}

	m, err := irc.ParseMessage(buf)
	if err != nil && err != irc.ErrTruncated {
		return irc.Message{}, fmt.Errorf("unable to parse message: %s: %s", buf,
			err)
	}

	return m, nil
}

// read reads a line from the connection.
func (c Client) read() (string, error) {
	if err := c.conn.SetDeadline(time.Now().Add(c.timeoutTime)); err != nil {
		return "", fmt.Errorf("unable to set deadline: %s", err)
	}

	line, err := c.rw.ReadString('\n')
	if err != nil {
		return "", err
	}

	log.Printf("Read: %s", strings.TrimRight(line, "\r\n"))

	return line, nil
}