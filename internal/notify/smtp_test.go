package notify

import (
	"bufio"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// mockSMTPServer starts a minimal SMTP server on a random port.
// It records received commands and returns the listener for cleanup.
func mockSMTPServer(t *testing.T, handler func(conn net.Conn)) (string, net.Listener) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to start mock SMTP server: %v", err)
	}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return // listener closed
			}
			go handler(conn)
		}
	}()

	return ln.Addr().String(), ln
}

// basicSMTPHandler handles a minimal SMTP conversation.
func basicSMTPHandler(conn net.Conn, mailCount *atomic.Int32) {
	defer conn.Close()

	fmt.Fprintf(conn, "220 mock.smtp.test ESMTP\r\n")
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		line := scanner.Text()
		cmd := strings.ToUpper(strings.SplitN(line, " ", 2)[0])
		switch cmd {
		case "EHLO", "HELO":
			fmt.Fprintf(conn, "250-mock.smtp.test\r\n")
			fmt.Fprintf(conn, "250 AUTH PLAIN LOGIN\r\n")
		case "AUTH":
			fmt.Fprintf(conn, "235 Authentication successful\r\n")
		case "MAIL":
			fmt.Fprintf(conn, "250 OK\r\n")
		case "RCPT":
			fmt.Fprintf(conn, "250 OK\r\n")
		case "DATA":
			fmt.Fprintf(conn, "354 Start mail input\r\n")
			// Read until lone "."
			for scanner.Scan() {
				if scanner.Text() == "." {
					break
				}
			}
			mailCount.Add(1)
			fmt.Fprintf(conn, "250 OK\r\n")
		case "QUIT":
			fmt.Fprintf(conn, "221 Bye\r\n")
			return
		default:
			fmt.Fprintf(conn, "500 Unknown command\r\n")
		}
	}
}

func TestNewSMTPMailer(t *testing.T) {
	cfg := SMTPConfig{
		Host:     "smtp.example.com",
		Port:     587,
		Username: "user@example.com",
		Password: "password",
		Protocol: "starttls",
		FromAddr: "user@example.com",
		FromName: "onWatch",
		ToAddrs:  []string{"admin@example.com"},
	}
	logger := slog.Default()

	mailer := NewSMTPMailer(cfg, logger)
	if mailer == nil {
		t.Fatal("Expected non-nil mailer")
	}
}

func TestSMTPMailer_Send_PlainSMTP(t *testing.T) {
	var mailCount atomic.Int32
	addr, ln := mockSMTPServer(t, func(conn net.Conn) {
		basicSMTPHandler(conn, &mailCount)
	})
	defer ln.Close()

	host, port := splitHostPort(t, addr)

	cfg := SMTPConfig{
		Host:     host,
		Port:     port,
		Username: "user@test.com",
		Password: "pass",
		Protocol: "none",
		FromAddr: "sender@test.com",
		FromName: "Test",
		ToAddrs:  []string{"recipient@test.com"},
	}

	mailer := NewSMTPMailer(cfg, slog.Default())
	err := mailer.Send("Test Subject", "Test Body")
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	if mailCount.Load() != 1 {
		t.Errorf("Expected 1 mail sent, got %d", mailCount.Load())
	}
}

func TestSMTPMailer_Send_MultipleRecipients(t *testing.T) {
	var mailCount atomic.Int32
	addr, ln := mockSMTPServer(t, func(conn net.Conn) {
		basicSMTPHandler(conn, &mailCount)
	})
	defer ln.Close()

	host, port := splitHostPort(t, addr)

	cfg := SMTPConfig{
		Host:     host,
		Port:     port,
		Username: "user@test.com",
		Password: "pass",
		Protocol: "none",
		FromAddr: "sender@test.com",
		FromName: "Test",
		ToAddrs:  []string{"a@test.com", "b@test.com", "c@test.com"},
	}

	mailer := NewSMTPMailer(cfg, slog.Default())
	err := mailer.Send("Multi", "Body")
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	if mailCount.Load() != 1 {
		t.Errorf("Expected 1 mail sent, got %d", mailCount.Load())
	}
}

func TestSMTPMailer_Send_AuthFailure(t *testing.T) {
	addr, ln := mockSMTPServer(t, func(conn net.Conn) {
		defer conn.Close()
		fmt.Fprintf(conn, "220 mock ESMTP\r\n")
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			line := scanner.Text()
			cmd := strings.ToUpper(strings.SplitN(line, " ", 2)[0])
			switch cmd {
			case "EHLO", "HELO":
				fmt.Fprintf(conn, "250-mock\r\n")
				fmt.Fprintf(conn, "250 AUTH PLAIN LOGIN\r\n")
			case "AUTH":
				fmt.Fprintf(conn, "535 Authentication failed\r\n")
			case "QUIT":
				fmt.Fprintf(conn, "221 Bye\r\n")
				return
			default:
				fmt.Fprintf(conn, "500 Unknown\r\n")
			}
		}
	})
	defer ln.Close()

	host, port := splitHostPort(t, addr)

	cfg := SMTPConfig{
		Host:     host,
		Port:     port,
		Username: "bad@test.com",
		Password: "wrong",
		Protocol: "none",
		FromAddr: "sender@test.com",
		FromName: "Test",
		ToAddrs:  []string{"recipient@test.com"},
	}

	mailer := NewSMTPMailer(cfg, slog.Default())
	err := mailer.Send("Subject", "Body")
	if err == nil {
		t.Error("Expected auth failure error")
	}
}

func TestSMTPMailer_Send_ConnectionRefused(t *testing.T) {
	cfg := SMTPConfig{
		Host:     "127.0.0.1",
		Port:     19999, // nothing listening here
		Username: "user@test.com",
		Password: "pass",
		Protocol: "none",
		FromAddr: "sender@test.com",
		FromName: "Test",
		ToAddrs:  []string{"recipient@test.com"},
	}

	mailer := NewSMTPMailer(cfg, slog.Default())
	err := mailer.Send("Subject", "Body")
	if err == nil {
		t.Error("Expected connection error")
	}
}

func TestSMTPMailer_TestConnection_Success(t *testing.T) {
	addr, ln := mockSMTPServer(t, func(conn net.Conn) {
		defer conn.Close()
		fmt.Fprintf(conn, "220 mock ESMTP\r\n")
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			line := scanner.Text()
			cmd := strings.ToUpper(strings.SplitN(line, " ", 2)[0])
			switch cmd {
			case "EHLO", "HELO":
				fmt.Fprintf(conn, "250-mock\r\n")
				fmt.Fprintf(conn, "250 AUTH PLAIN LOGIN\r\n")
			case "AUTH":
				fmt.Fprintf(conn, "235 OK\r\n")
			case "QUIT":
				fmt.Fprintf(conn, "221 Bye\r\n")
				return
			default:
				fmt.Fprintf(conn, "500 Unknown\r\n")
			}
		}
	})
	defer ln.Close()

	host, port := splitHostPort(t, addr)

	cfg := SMTPConfig{
		Host:     host,
		Port:     port,
		Username: "user@test.com",
		Password: "pass",
		Protocol: "none",
		FromAddr: "sender@test.com",
		FromName: "Test",
		ToAddrs:  []string{"recipient@test.com"},
	}

	mailer := NewSMTPMailer(cfg, slog.Default())
	err := mailer.TestConnection()
	if err != nil {
		t.Fatalf("TestConnection failed: %v", err)
	}
}

func TestSMTPMailer_TestConnection_Failure(t *testing.T) {
	cfg := SMTPConfig{
		Host:     "127.0.0.1",
		Port:     19998,
		Protocol: "none",
		FromAddr: "sender@test.com",
		FromName: "Test",
		ToAddrs:  []string{"recipient@test.com"},
	}

	mailer := NewSMTPMailer(cfg, slog.Default())
	err := mailer.TestConnection()
	if err == nil {
		t.Error("Expected connection failure")
	}
}

func TestSMTPMailer_Send_VerifyHeaders(t *testing.T) {
	var receivedData string
	var mu sync.Mutex

	addr, ln := mockSMTPServer(t, func(conn net.Conn) {
		defer conn.Close()
		fmt.Fprintf(conn, "220 mock ESMTP\r\n")
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			line := scanner.Text()
			cmd := strings.ToUpper(strings.SplitN(line, " ", 2)[0])
			switch cmd {
			case "EHLO", "HELO":
				fmt.Fprintf(conn, "250-mock\r\n")
				fmt.Fprintf(conn, "250 AUTH PLAIN LOGIN\r\n")
			case "AUTH":
				fmt.Fprintf(conn, "235 OK\r\n")
			case "MAIL":
				fmt.Fprintf(conn, "250 OK\r\n")
			case "RCPT":
				fmt.Fprintf(conn, "250 OK\r\n")
			case "DATA":
				fmt.Fprintf(conn, "354 Go ahead\r\n")
				var sb strings.Builder
				for scanner.Scan() {
					if scanner.Text() == "." {
						break
					}
					sb.WriteString(scanner.Text())
					sb.WriteString("\r\n")
				}
				mu.Lock()
				receivedData = sb.String()
				mu.Unlock()
				fmt.Fprintf(conn, "250 OK\r\n")
			case "QUIT":
				fmt.Fprintf(conn, "221 Bye\r\n")
				return
			default:
				fmt.Fprintf(conn, "500 Unknown\r\n")
			}
		}
	})
	defer ln.Close()

	host, port := splitHostPort(t, addr)

	cfg := SMTPConfig{
		Host:     host,
		Port:     port,
		Username: "user@test.com",
		Password: "pass",
		Protocol: "none",
		FromAddr: "alerts@onwatch.dev",
		FromName: "onWatch Alerts",
		ToAddrs:  []string{"admin@example.com"},
	}

	mailer := NewSMTPMailer(cfg, slog.Default())
	err := mailer.Send("Quota Alert: 5-Hour Limit at 80%", "Your quota is approaching the limit.")
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	mu.Lock()
	data := receivedData
	mu.Unlock()

	if !strings.Contains(data, "From: onWatch Alerts <alerts@onwatch.dev>") {
		t.Errorf("Missing or incorrect From header in:\n%s", data)
	}
	if !strings.Contains(data, "To: admin@example.com") {
		t.Errorf("Missing or incorrect To header in:\n%s", data)
	}
	if !strings.Contains(data, "Subject: Quota Alert: 5-Hour Limit at 80%") {
		t.Errorf("Missing or incorrect Subject header in:\n%s", data)
	}
	if !strings.Contains(data, "Content-Type: text/plain; charset=UTF-8") {
		t.Errorf("Missing Content-Type header in:\n%s", data)
	}
	if !strings.Contains(data, "Your quota is approaching the limit.") {
		t.Errorf("Missing body in:\n%s", data)
	}
}

// splitHostPort is a test helper to split "host:port" into parts.
func splitHostPort(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("Failed to split host:port: %v", err)
	}
	var port int
	fmt.Sscanf(portStr, "%d", &port)
	return host, port
}
