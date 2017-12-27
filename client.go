package boxcat

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

	writeTimeout time.Duration
	readTimeout  time.Duration

	conn net.Conn
	rw   *bufio.ReadWriter

	recvChan chan irc.Message
	sendChan chan irc.Message
	errChan  chan error
	doneChan chan struct{}
	wg       *sync.WaitGroup
}

func newClient(nick, serverHost string, serverPort uint16) *Client {
	return &Client{
		Nick:       nick,
		ServerHost: serverHost,
		ServerPort: serverPort,

		writeTimeout: 30 * time.Second,
		readTimeout:  100 * time.Millisecond,
	}
}

// start starts a connection and registers.
//
// The client responds to PING commands.
//
// All messages received from the server will be sent on the receive channel.
//
// Messages you send to the send channel will be sent to the server.
//
// If an error occurs, we send a message on the error channel. If you receive a
// message on that channel, you must stop the client.
//
// The caller must call stop() to clean up the client.
func (c *Client) start() (
	<-chan irc.Message,
	chan<- irc.Message,
	<-chan error,
	error,
) {
	if err := c.connect(); err != nil {
		return nil, nil, nil, fmt.Errorf("error connecting: %s", err)
	}

	if err := c.writeMessage(irc.Message{
		Command: "NICK",
		Params:  []string{c.Nick},
	}); err != nil {
		_ = c.conn.Close()
		return nil, nil, nil, err
	}

	if err := c.writeMessage(irc.Message{
		Command: "USER",
		Params:  []string{c.Nick, "0", "*", c.Nick},
	}); err != nil {
		_ = c.conn.Close()
		return nil, nil, nil, err
	}

	c.recvChan = make(chan irc.Message, 512)
	c.sendChan = make(chan irc.Message, 512)
	c.errChan = make(chan error, 512)
	c.doneChan = make(chan struct{})

	c.wg = &sync.WaitGroup{}

	c.wg.Add(1)
	go c.reader(c.recvChan)

	// Writer
	c.wg.Add(1)
	go c.writer(c.sendChan)

	return c.recvChan, c.sendChan, c.errChan, nil
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

func (c Client) reader(recvChan chan<- irc.Message) {
	defer c.wg.Done()

	for {
		select {
		case <-c.doneChan:
			close(recvChan)
			return
		default:
		}

		m, err := c.readMessage()
		if err != nil {
			// If we time out waiting for a read to succeed, just ignore it and try
			// again. We want a short timeout on that so we frequently check whether
			// we should end.
			//
			// There's no accessible error variable to compare with
			if strings.Contains(err.Error(), "i/o timeout") {
				continue
			}

			c.errChan <- fmt.Errorf("error reading message: %s", err)
			close(recvChan)
			return
		}

		if m.Command == "PING" {
			if err := c.writeMessage(irc.Message{
				Command: "PONG",
				Params:  []string{m.Params[0]},
			}); err != nil {
				c.errChan <- fmt.Errorf("error sending pong: %s", err)
				close(recvChan)
				return
			}
		}

		recvChan <- m
	}
}

func (c Client) writer(sendChan <-chan irc.Message) {
	defer c.wg.Done()

LOOP:
	for {
		select {
		case <-c.doneChan:
			break LOOP
		case m, ok := <-sendChan:
			if !ok {
				break
			}
			if err := c.writeMessage(m); err != nil {
				c.errChan <- fmt.Errorf("error writing message: %s", err)
				break
			}
		}
	}

	for range sendChan {
	}
}

// writeMessage writes an IRC message to the connection.
func (c Client) writeMessage(m irc.Message) error {
	buf, err := m.Encode()
	if err != nil && err != irc.ErrTruncated {
		return fmt.Errorf("unable to encode message: %s", err)
	}

	if err := c.conn.SetWriteDeadline(time.Now().Add(
		c.writeTimeout)); err != nil {
		return fmt.Errorf("unable to set deadline: %s", err)
	}

	sz, err := c.rw.WriteString(buf)
	if err != nil {
		return err
	}

	if sz != len(buf) {
		return fmt.Errorf("short write")
	}

	if err := c.rw.Flush(); err != nil {
		return fmt.Errorf("flush error: %s", err)
	}

	log.Printf("client %s: sent: %s", c.Nick, strings.TrimRight(buf, "\r\n"))
	return nil
}

// readMessage reads a line from the connection and parses it as an IRC message.
func (c Client) readMessage() (irc.Message, error) {
	if err := c.conn.SetReadDeadline(time.Now().Add(c.readTimeout)); err != nil {
		return irc.Message{}, fmt.Errorf("unable to set deadline: %s", err)
	}

	line, err := c.rw.ReadString('\n')
	if err != nil {
		return irc.Message{}, err
	}

	log.Printf("client %s: read: %s", c.Nick, strings.TrimRight(line, "\r\n"))

	m, err := irc.ParseMessage(line)
	if err != nil && err != irc.ErrTruncated {
		return irc.Message{}, fmt.Errorf("unable to parse message: %s: %s", line,
			err)
	}

	return m, nil
}

// Shut down the client and clean up.
//
// You must not send any messages on the send channel after calling this
// function.
func (c *Client) stop() {
	// Tell reader and writer to end.
	close(c.doneChan)

	close(c.sendChan)

	// Wait for reader and writer to end.
	c.wg.Wait()

	// We know the reader and writer won't be sending on the error channel any
	// more.
	close(c.errChan)

	_ = c.conn.Close()

	for range c.recvChan {
	}
	for range c.errChan {
	}
}
