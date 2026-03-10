package notify

import (
	"bufio"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"net/smtp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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

func mockSTARTTLSSMTPServer(t *testing.T, mailCount *atomic.Int32, tlsUsed *atomic.Bool) (string, *x509.CertPool, net.Listener) {
	t.Helper()

	serverTLS, rootCAs := newTestTLSConfig(t, "127.0.0.1")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to start STARTTLS SMTP server: %v", err)
	}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go startTLSSMTPHandler(conn, serverTLS, mailCount, tlsUsed)
		}
	}()

	return ln.Addr().String(), rootCAs, ln
}

func mockImplicitTLSSMTPServer(t *testing.T, mailCount *atomic.Int32) (string, *x509.CertPool, net.Listener) {
	t.Helper()

	serverTLS, rootCAs := newTestTLSConfig(t, "127.0.0.1")
	ln, err := tls.Listen("tcp", "127.0.0.1:0", serverTLS)
	if err != nil {
		t.Fatalf("Failed to start implicit TLS SMTP server: %v", err)
	}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go basicSMTPHandler(conn, mailCount)
		}
	}()

	return ln.Addr().String(), rootCAs, ln
}

func startTLSSMTPHandler(conn net.Conn, serverTLS *tls.Config, mailCount *atomic.Int32, tlsUsed *atomic.Bool) {
	defer conn.Close()

	currentConn := conn
	reader := bufio.NewReader(currentConn)
	writer := bufio.NewWriter(currentConn)
	tlsActive := false

	writeLine := func(format string, args ...any) bool {
		if _, err := fmt.Fprintf(writer, format, args...); err != nil {
			return false
		}
		return writer.Flush() == nil
	}

	if !writeLine("220 mock.smtp.test ESMTP\r\n") {
		return
	}

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		cmd := strings.ToUpper(strings.SplitN(line, " ", 2)[0])
		switch cmd {
		case "EHLO", "HELO":
			if !writeLine("250-mock.smtp.test\r\n") {
				return
			}
			if !tlsActive {
				if !writeLine("250-STARTTLS\r\n") {
					return
				}
			}
			if !writeLine("250 AUTH PLAIN LOGIN\r\n") {
				return
			}
		case "STARTTLS":
			if !writeLine("220 Ready to start TLS\r\n") {
				return
			}
			tlsConn := tls.Server(currentConn, serverTLS)
			if err := tlsConn.Handshake(); err != nil {
				return
			}
			if tlsUsed != nil {
				tlsUsed.Store(true)
			}
			currentConn = tlsConn
			reader = bufio.NewReader(currentConn)
			writer = bufio.NewWriter(currentConn)
			tlsActive = true
		case "AUTH":
			if !writeLine("235 Authentication successful\r\n") {
				return
			}
		case "MAIL":
			if !writeLine("250 OK\r\n") {
				return
			}
		case "RCPT":
			if !writeLine("250 OK\r\n") {
				return
			}
		case "DATA":
			if !writeLine("354 Start mail input\r\n") {
				return
			}
			for {
				dataLine, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if strings.TrimRight(dataLine, "\r\n") == "." {
					break
				}
			}
			mailCount.Add(1)
			if !writeLine("250 OK\r\n") {
				return
			}
		case "QUIT":
			writeLine("221 Bye\r\n")
			return
		default:
			if !writeLine("500 Unknown command\r\n") {
				return
			}
		}
	}
}

func newTestTLSConfig(t *testing.T, host string) (*tls.Config, *x509.CertPool) {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("Failed to generate private key: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: host,
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	if ip := net.ParseIP(host); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{host}
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("Failed to create certificate: %v", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("Failed to load key pair: %v", err)
	}

	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certPEM) {
		t.Fatal("Failed to append test certificate to root pool")
	}

	return &tls.Config{Certificates: []tls.Certificate{cert}}, roots
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

func TestShouldRetryWithNegotiatedSMTP(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "fallback on tls handshake mismatch",
			err:  fmt.Errorf("tls: first record does not look like a TLS handshake"),
			want: true,
		},
		{
			name: "no fallback on certificate errors",
			err:  fmt.Errorf("x509: certificate signed by unknown authority"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldRetryWithNegotiatedSMTP(tt.err)
			if got != tt.want {
				t.Fatalf("shouldRetryWithNegotiatedSMTP(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestSMTPMailer_Send_AutoNegotiatesSTARTTLS(t *testing.T) {
	var mailCount atomic.Int32
	var tlsUsed atomic.Bool

	addr, roots, ln := mockSTARTTLSSMTPServer(t, &mailCount, &tlsUsed)
	defer ln.Close()

	host, port := splitHostPort(t, addr)

	cfg := SMTPConfig{
		Host:     host,
		Port:     port,
		Username: "user@test.com",
		Password: "pass",
		Protocol: "auto",
		FromAddr: "sender@test.com",
		FromName: "Test",
		ToAddrs:  []string{"recipient@test.com"},
	}

	mailer := NewSMTPMailer(cfg, slog.Default())
	mailer.tlsConfig = &tls.Config{RootCAs: roots, ServerName: host}

	if err := mailer.Send("Subject", "Body"); err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if !tlsUsed.Load() {
		t.Fatal("expected auto mode to upgrade the connection with STARTTLS")
	}
	if mailCount.Load() != 1 {
		t.Fatalf("expected 1 email sent, got %d", mailCount.Load())
	}
}

func TestSMTPMailer_Send_ImplicitTLS(t *testing.T) {
	var mailCount atomic.Int32

	addr, roots, ln := mockImplicitTLSSMTPServer(t, &mailCount)
	defer ln.Close()

	host, port := splitHostPort(t, addr)

	cfg := SMTPConfig{
		Host:     host,
		Port:     port,
		Username: "user@test.com",
		Password: "pass",
		Protocol: "tls",
		FromAddr: "sender@test.com",
		FromName: "Test",
		ToAddrs:  []string{"recipient@test.com"},
	}

	mailer := NewSMTPMailer(cfg, slog.Default())
	mailer.tlsConfig = &tls.Config{RootCAs: roots, ServerName: host}

	if err := mailer.Send("Subject", "Body"); err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if mailCount.Load() != 1 {
		t.Fatalf("expected 1 email sent, got %d", mailCount.Load())
	}
}

func TestSMTPMailer_Send_TLSFallsBackToNegotiatedSMTP(t *testing.T) {
	var mailCount atomic.Int32
	var tlsUsed atomic.Bool

	addr, roots, ln := mockSTARTTLSSMTPServer(t, &mailCount, &tlsUsed)
	defer ln.Close()

	host, port := splitHostPort(t, addr)

	cfg := SMTPConfig{
		Host:     host,
		Port:     port,
		Username: "user@test.com",
		Password: "pass",
		Protocol: "tls",
		FromAddr: "sender@test.com",
		FromName: "Test",
		ToAddrs:  []string{"recipient@test.com"},
	}

	mailer := NewSMTPMailer(cfg, slog.Default())
	mailer.tlsConfig = &tls.Config{RootCAs: roots, ServerName: host}

	if err := mailer.Send("Subject", "Body"); err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if !tlsUsed.Load() {
		t.Fatal("expected explicit TLS fallback to recover via negotiated SMTP")
	}
}

func TestSMTPMailer_Authenticate_NoneAllowsPlaintextRemoteAuth(t *testing.T) {
	addr, ln := mockSMTPServer(t, func(conn net.Conn) {
		defer conn.Close()
		fmt.Fprintf(conn, "220 mock ESMTP\r\n")
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			cmd := strings.ToUpper(strings.SplitN(scanner.Text(), " ", 2)[0])
			switch cmd {
			case "EHLO", "HELO":
				fmt.Fprintf(conn, "250-mock\r\n")
				fmt.Fprintf(conn, "250 AUTH PLAIN LOGIN\r\n")
			case "AUTH":
				fmt.Fprintf(conn, "235 Authentication successful\r\n")
			case "QUIT":
				fmt.Fprintf(conn, "221 Bye\r\n")
				return
			default:
				fmt.Fprintf(conn, "500 Unknown\r\n")
			}
		}
	})
	defer ln.Close()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	client, err := smtp.NewClient(conn, "smtp.example.com")
	if err != nil {
		t.Fatalf("smtp client failed: %v", err)
	}
	defer client.Close()

	mailer := NewSMTPMailer(SMTPConfig{
		Host:     "smtp.example.com",
		Port:     25,
		Username: "user@test.com",
		Password: "pass",
		Protocol: "none",
	}, slog.Default())

	if err := mailer.authenticate(client, false); err != nil {
		t.Fatalf("authenticate failed: %v", err)
	}
}

func TestSMTPMailer_Authenticate_AutoRejectsPlaintextRemoteAuth(t *testing.T) {
	addr, ln := mockSMTPServer(t, func(conn net.Conn) {
		defer conn.Close()
		fmt.Fprintf(conn, "220 mock ESMTP\r\n")
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			cmd := strings.ToUpper(strings.SplitN(scanner.Text(), " ", 2)[0])
			switch cmd {
			case "EHLO", "HELO":
				fmt.Fprintf(conn, "250-mock\r\n")
				fmt.Fprintf(conn, "250 AUTH PLAIN LOGIN\r\n")
			case "QUIT":
				fmt.Fprintf(conn, "221 Bye\r\n")
				return
			default:
				fmt.Fprintf(conn, "500 Unknown\r\n")
			}
		}
	})
	defer ln.Close()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	client, err := smtp.NewClient(conn, "smtp.example.com")
	if err != nil {
		t.Fatalf("smtp client failed: %v", err)
	}
	defer client.Close()

	mailer := NewSMTPMailer(SMTPConfig{
		Host:     "smtp.example.com",
		Port:     25,
		Username: "user@test.com",
		Password: "pass",
		Protocol: "auto",
	}, slog.Default())

	err = mailer.authenticate(client, false)
	if err == nil || !strings.Contains(err.Error(), "select None") {
		t.Fatalf("expected auto mode to reject plaintext remote auth, got %v", err)
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
