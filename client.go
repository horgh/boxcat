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

	conn         net.Conn
	rw           *bufio.ReadWriter
	writeTimeout time.Duration
	readTimeout  time.Duration
}

func newClient(nick, serverHost string, serverPort uint16) *Client {
	return &Client{
		Nick:       nick,
		ServerHost: serverHost,
		ServerPort: serverPort,

		writeTimeout: 30 * time.Second,
		readTimeout:  time.Second,
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
//
// The receive channel will close when an error occurs.
//
// The caller should close the send channel to clean up.
//
// The caller should drain the error channel after closing the send channel.
func (c *Client) start() (<-chan irc.Message, chan<- irc.Message, <-chan error,
	chan struct{}, error) {
	if err := c.connect(); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("error connecting: %s", err)
	}

	if err := c.writeMessage(irc.Message{
		Command: "NICK",
		Params:  []string{c.Nick},
	}); err != nil {
		return nil, nil, nil, nil, err
	}
	if err := c.writeMessage(irc.Message{
		Command: "USER",
		Params:  []string{c.Nick, "0", "*", c.Nick},
	}); err != nil {
		return nil, nil, nil, nil, err
	}

	recvChan := make(chan irc.Message, 512)
	sendChan := make(chan irc.Message, 512)
	errChan := make(chan error, 512)
	doneChan := make(chan struct{})

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-doneChan:
				close(recvChan)
				return
			default:
			}

			m, err := c.readMessage()
			if err != nil {
				// There's no accessible error variable to compare with
				if strings.Contains(err.Error(), "i/o timeout") {
					continue
				}
				errChan <- fmt.Errorf("error reading message: %s", err)
				close(recvChan)
				return
			}

			if m.Command == "PING" {
				if err := c.writeMessage(irc.Message{
					Command: "PONG",
					Params:  []string{m.Params[0]},
				}); err != nil {
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

	return recvChan, sendChan, errChan, doneChan, nil
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
	if err := c.conn.SetReadDeadline(time.Now().Add(c.readTimeout)); err != nil {
		return "", fmt.Errorf("unable to set deadline: %s", err)
	}

	line, err := c.rw.ReadString('\n')
	if err != nil {
		return "", err
	}

	log.Printf("client %s: read: %s", c.Nick, strings.TrimRight(line, "\r\n"))

	return line, nil
}
