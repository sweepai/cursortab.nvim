package main

import (
	"cursortab/logger"
	"fmt"
	"io"
	"net"
	"os"
	"time"
)

type Client struct {
	socketPath string
}

func NewClient() *Client {
	return &Client{
		socketPath: getSocketPath(),
	}
}

func (c *Client) Connect() error {
	// Connect to daemon
	conn, err := net.Dial("unix", c.socketPath)
	if err != nil {
		return err
	}
	defer conn.Close()

	// Relay between stdin/stdout and socket
	go func() {
		io.Copy(conn, os.Stdin)
		conn.Close()
	}()

	io.Copy(os.Stdout, conn)
	return nil
}

func (c *Client) EnsureDaemonRunning() error {
	running, pid := isDaemonRunning()
	if running {
		logger.Debug("daemon already running with PID %d", pid)
		return nil
	}

	return c.startDaemon()
}

func (c *Client) startDaemon() error {
	logger.Debug("starting daemon...")

	// Start daemon in background
	cmd := []string{os.Args[0], "--daemon"}
	env := os.Environ()

	// Start the daemon process
	_, err := os.StartProcess(os.Args[0], cmd, &os.ProcAttr{
		Env: env,
		Files: []*os.File{
			nil, // stdin
			nil, // stdout
			nil, // stderr
		},
	})
	if err != nil {
		return err
	}

	// Wait for daemon to start
	return c.waitForDaemon()
}

func (c *Client) waitForDaemon() error {
	for range 50 { // Wait up to 5 seconds
		if running, _ := isDaemonRunning(); running {
			logger.Debug("daemon started successfully")
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("daemon failed to start within timeout")
}
