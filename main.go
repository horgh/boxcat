package main

import (
	"bufio"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/horgh/irc"
)

func main() {
	catbox, err := harnessCatbox()
	if err != nil {
		log.Fatalf("error harnessing catbox: %s", err)
	}

	nick1 := "client1"
	client1 := newClient(nick1, "127.0.0.1", catbox.Port)
	recvChan1, sendChan1, errChan1, err := client1.start()
	if err != nil {
		log.Printf("error starting client: %s", err)
		catbox.stop()
		return
	}

	for m := range recvChan1 {
		log.Printf("got command %s", m)
	}

	close(sendChan1)

	for err := range errChan1 {
		log.Printf("got err: %s", err)
	}

	catbox.stop()
}

// Catbox holds information about a harnessed catbox.
type Catbox struct {
	Port      uint16
	Stderr    io.ReadCloser
	Stdout    io.ReadCloser
	Command   *exec.Cmd
	WaitGroup *sync.WaitGroup
}

var catboxDir = filepath.Join(os.Getenv("GOPATH"), "src", "github.com", "horgh",
	"catbox")

func harnessCatbox() (*Catbox, error) {
	if err := buildCatbox(); err != nil {
		return nil, fmt.Errorf("error building catbox: %s", err)
	}

	catbox, err := startCatbox()
	if err != nil {
		return nil, fmt.Errorf("error starting catbox: %s", err)
	}

	var wg sync.WaitGroup

	wg.Add(1)
	go logReader(&wg, "catbox stderr", catbox.Stderr)
	wg.Add(1)
	go logReader(&wg, "catbox stdout", catbox.Stdout)

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := catbox.Command.Wait(); err != nil {
			log.Printf("error from catbox: %s", err)
		}
	}()

	catbox.WaitGroup = &wg

	return catbox, nil
}

var builtCatbox bool

func buildCatbox() error {
	if builtCatbox {
		return nil
	}

	cmd := exec.Command("go", "build")
	cmd.Dir = catboxDir

	log.Printf("Running %s in [%s]...", cmd.Args, cmd.Dir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("error building catbox: %s: %s", err, output)
	}

	builtCatbox = true
	return nil
}

func startCatbox() (*Catbox, error) {
	tmpDir, err := ioutil.TempDir("", "boxcat-")
	if err != nil {
		return nil, fmt.Errorf("error retrieving a temporary directory: %s", err)
	}
	defer func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			log.Fatalf("error cleaning up temporary directory: %s", err)
		}
	}()

	catboxConf := filepath.Join(tmpDir, "catbox.conf")

	// Retry as there's a race for our random port.
	for i := 0; i < 3; i++ {
		port, err := getRandomPort()
		if err != nil {
			return nil, fmt.Errorf("error finding random port to listen on: %s", err)
		}

		catbox, err := tryToStartCatbox(catboxConf, port)
		if err == nil {
			return catbox, nil
		}
	}

	return nil, fmt.Errorf("gave up trying to start catbox")
}

func getRandomPort() (uint16, error) {
	ln, err := net.Listen("tcp4", "127.0.0.1:")
	if err != nil {
		return 0, fmt.Errorf("error opening a random port: %s", err)
	}

	addr := ln.Addr().String()
	colonIndex := strings.Index(addr, ":")
	portString := addr[colonIndex+1:]
	port, err := strconv.ParseUint(portString, 10, 16)
	if err != nil {
		_ = ln.Close()
		return 0, fmt.Errorf("error parsing port: %s", err)
	}

	if err := ln.Close(); err != nil {
		return 0, fmt.Errorf("error closing listener: %s", err)
	}

	return uint16(port), nil
}

func tryToStartCatbox(conf string, port uint16) (*Catbox, error) {
	buf := fmt.Sprintf(`listen-port = %d`, port)

	if err := ioutil.WriteFile(conf, []byte(buf), 0644); err != nil {
		return nil, fmt.Errorf("error writing conf: %s", err)
	}

	cmd := exec.Command("./catbox", "-conf", conf)
	cmd.Dir = catboxDir

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("error retrieving stderr pipe: %s", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stderr.Close()
		return nil, fmt.Errorf("error retrieving stdout pipe: %s", err)
	}

	if err := cmd.Start(); err != nil {
		// Try again in case it was a problem with the port being taken
		_ = stderr.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("error starting: %s", err)
	}

	address := fmt.Sprintf("127.0.0.1:%d", port)

	for waited := time.Duration(0); waited < 3*time.Second; {
		conn, err := net.DialTimeout("tcp4", address, 100*time.Millisecond)
		if err != nil {
			time.Sleep(100 * time.Millisecond)
			waited += 100 * time.Millisecond
			continue
		}

		_ = conn.Close()

		return &Catbox{
			Port:    port,
			Command: cmd,
			Stderr:  stderr,
			Stdout:  stdout,
		}, nil
	}

	stderrOutput, err := ioutil.ReadAll(stderr)
	if err == nil && len(stderrOutput) != 0 {
		log.Printf("catbox stderr: %s", stderrOutput)
	}
	_ = stderr.Close()

	stdoutOutput, err := ioutil.ReadAll(stdout)
	if err == nil && len(stdoutOutput) != 0 {
		log.Printf("catbox stdout: %s", stdoutOutput)
	}
	_ = stdout.Close()

	return nil, fmt.Errorf("catbox failed to start")
}

func logReader(wg *sync.WaitGroup, prefix string, r io.Reader) {
	defer wg.Done()

	scanner := bufio.NewScanner(r)

	for scanner.Scan() {
		line := scanner.Text()
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		log.Printf("%s: %s", prefix, line)
	}

	if err := scanner.Err(); err != nil {
		log.Fatalf("error scanning: %s", err)
	}
}

func (c *Catbox) stop() {
	if err := c.Command.Process.Kill(); err != nil {
		log.Printf("error killing catbox: %s", err)
	}
	c.WaitGroup.Wait()
}

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
