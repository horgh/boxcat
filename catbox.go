package boxcat

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
)

// Catbox holds information about a harnessed catbox.
type Catbox struct {
	Port      uint16
	Stderr    io.ReadCloser
	Stdout    io.ReadCloser
	Command   *exec.Cmd
	WaitGroup *sync.WaitGroup
	ConfigDir string
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
			log.Printf("catbox exited: %s", err)
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

	catboxConf := filepath.Join(tmpDir, "catbox.conf")

	listener, port, err := getRandomPort()
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("error opening random port: %s", err)
	}

	catbox, err := runCatbox(catboxConf, listener, port)
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		_ = listener.Close()
		return nil, fmt.Errorf("error running catbox: %s", err)
	}

	catbox.ConfigDir = tmpDir
	return catbox, nil
}

func getRandomPort() (net.Listener, uint16, error) {
	ln, err := net.Listen("tcp4", "127.0.0.1:")
	if err != nil {
		return nil, 0, fmt.Errorf("error opening a random port: %s", err)
	}

	addr := ln.Addr().String()
	colonIndex := strings.Index(addr, ":")
	portString := addr[colonIndex+1:]
	port, err := strconv.ParseUint(portString, 10, 16)
	if err != nil {
		_ = ln.Close()
		return nil, 0, fmt.Errorf("error parsing port: %s", err)
	}

	return ln, uint16(port), nil
}

func runCatbox(conf string, ln net.Listener, port uint16) (*Catbox, error) {
	// -1 because we pass in fd.
	buf := fmt.Sprintf(`
listen-port = %d
`, -1)

	if err := ioutil.WriteFile(conf, []byte(buf), 0644); err != nil {
		return nil, fmt.Errorf("error writing conf: %s", err)
	}

	cmd := exec.Command("./catbox",
		"-conf", conf,
		"-listen-fd", "3",
	)

	cmd.Dir = catboxDir

	f, err := ln.(*net.TCPListener).File()
	if err != nil {
		return nil, fmt.Errorf("error retrieving listener file: %s", err)
	}
	cmd.ExtraFiles = []*os.File{f}

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

	if err := os.RemoveAll(c.ConfigDir); err != nil {
		log.Fatalf("error cleaning up temporary directory: %s", err)
	}
}
