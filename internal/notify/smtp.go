package notify

import (
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// SMTPConfig holds SMTP connection settings.
type SMTPConfig struct {
	Host     string   // SMTP server hostname
	Port     int      // SMTP server port (25, 465, 587)
	Username string   // SMTP auth username
	Password string   // SMTP auth password (plaintext or decrypted)
	Protocol string   // "auto", "tls" (implicit TLS), "starttls" (explicit upgrade), "none" (plaintext)
	FromAddr string   // Sender email address
	FromName string   // Sender display name
	ToAddrs  []string // Recipient email addresses
}

// SMTPMailer sends email notifications via SMTP.
type SMTPMailer struct {
	config SMTPConfig
	logger *slog.Logger
	// tlsConfig is a test hook for injecting trusted roots.
	tlsConfig *tls.Config
}

// NewSMTPMailer creates a new SMTP mailer with the given config.
func NewSMTPMailer(cfg SMTPConfig, logger *slog.Logger) *SMTPMailer {
	if normalizedSMTPProtocol(cfg.Protocol) == "none" && logger != nil {
		logger.Warn("SMTP using unencrypted connection. If authentication is enabled, credentials may be sent in plaintext.")
	}
	return &SMTPMailer{config: cfg, logger: logger}
}

// Send sends an email with the given subject and plaintext body.
func (m *SMTPMailer) Send(subject, body string) error {
	msg := m.buildMessage(subject, body)

	client, encrypted, err := m.connect()
	if err != nil {
		return fmt.Errorf("notify.Send: connect: %w", err)
	}
	defer client.Close()

	if err := m.authenticate(client, encrypted); err != nil {
		return fmt.Errorf("notify.Send: auth: %w", err)
	}

	if err := client.Mail(m.config.FromAddr); err != nil {
		return fmt.Errorf("notify.Send: MAIL FROM: %w", err)
	}

	for _, addr := range m.config.ToAddrs {
		if err := client.Rcpt(addr); err != nil {
			return fmt.Errorf("notify.Send: RCPT TO %s: %w", addr, err)
		}
	}

	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("notify.Send: DATA: %w", err)
	}

	if _, err := w.Write([]byte(msg)); err != nil {
		return fmt.Errorf("notify.Send: write: %w", err)
	}

	if err := w.Close(); err != nil {
		return fmt.Errorf("notify.Send: close data: %w", err)
	}

	client.Quit()
	m.logger.Info("email sent", "subject", subject, "recipients", len(m.config.ToAddrs))
	return nil
}

// TestConnection verifies SMTP connectivity and authentication.
func (m *SMTPMailer) TestConnection() error {
	client, encrypted, err := m.connect()
	if err != nil {
		return fmt.Errorf("notify.TestConnection: connect: %w", err)
	}
	defer client.Close()

	if m.config.Username != "" {
		if err := m.authenticate(client, encrypted); err != nil {
			return fmt.Errorf("notify.TestConnection: auth: %w", err)
		}
	}

	client.Quit()
	return nil
}

// connect establishes an SMTP connection using the configured protocol.
func (m *SMTPMailer) connect() (*smtp.Client, bool, error) {
	addr := net.JoinHostPort(m.config.Host, fmt.Sprintf("%d", m.config.Port))
	dialer := &net.Dialer{Timeout: 10 * time.Second}

	switch normalizedSMTPProtocol(m.config.Protocol) {
	case "auto":
		return m.connectAuto(addr, dialer)
	case "tls":
		client, encrypted, err := m.connectTLS(addr, dialer)
		if err == nil {
			return client, encrypted, nil
		}
		if shouldRetryWithNegotiatedSMTP(err) {
			if m.logger != nil {
				m.logger.Warn("SMTP implicit TLS failed; retrying with negotiated SMTP",
					"host", m.config.Host,
					"port", m.config.Port,
				)
			}
			client, encrypted, fallbackErr := m.connectNegotiated(addr, dialer)
			if fallbackErr == nil {
				return client, encrypted, nil
			}
			return nil, false, fmt.Errorf("implicit TLS: %w; negotiated fallback: %v", err, fallbackErr)
		}
		return nil, false, fmt.Errorf("implicit TLS: %w", err)

	case "starttls":
		return m.connectSTARTTLS(addr, dialer)

	default:
		return m.connectPlain(addr, dialer)
	}
}

func (m *SMTPMailer) connectAuto(addr string, dialer *net.Dialer) (*smtp.Client, bool, error) {
	if m.config.Port == 465 {
		client, encrypted, err := m.connectTLS(addr, dialer)
		if err == nil {
			return client, encrypted, nil
		}
		if !shouldRetryWithNegotiatedSMTP(err) {
			return nil, false, fmt.Errorf("implicit TLS: %w", err)
		}
		if m.logger != nil {
			m.logger.Warn("SMTP auto mode falling back from implicit TLS to negotiated SMTP",
				"host", m.config.Host,
				"port", m.config.Port,
			)
		}
		client, encrypted, fallbackErr := m.connectNegotiated(addr, dialer)
		if fallbackErr == nil {
			return client, encrypted, nil
		}
		return nil, false, fmt.Errorf("implicit TLS: %w; negotiated fallback: %v", err, fallbackErr)
	}

	client, encrypted, err := m.connectNegotiated(addr, dialer)
	if err != nil {
		return nil, false, err
	}
	return client, encrypted, nil
}

func (m *SMTPMailer) connectTLS(addr string, dialer *net.Dialer) (*smtp.Client, bool, error) {
	// Implicit TLS (port 465): TLS from the start
	tlsConn, err := tls.DialWithDialer(dialer, "tcp", addr, m.clientTLSConfig())
	if err != nil {
		return nil, false, err
	}
	client, err := smtp.NewClient(tlsConn, m.config.Host)
	if err != nil {
		tlsConn.Close()
		return nil, false, fmt.Errorf("SMTP client: %w", err)
	}
	return client, true, nil
}

func (m *SMTPMailer) connectSTARTTLS(addr string, dialer *net.Dialer) (*smtp.Client, bool, error) {
	// STARTTLS (port 587): plain connect, then upgrade
	conn, err := dialer.Dial("tcp", addr)
	if err != nil {
		return nil, false, fmt.Errorf("dial: %w", err)
	}
	client, err := smtp.NewClient(conn, m.config.Host)
	if err != nil {
		conn.Close()
		return nil, false, fmt.Errorf("SMTP client: %w", err)
	}
	if err := client.StartTLS(m.clientTLSConfig()); err != nil {
		client.Close()
		return nil, false, fmt.Errorf("STARTTLS: %w", err)
	}
	return client, true, nil
}

func (m *SMTPMailer) connectNegotiated(addr string, dialer *net.Dialer) (*smtp.Client, bool, error) {
	client, encrypted, err := m.connectPlain(addr, dialer)
	if err != nil {
		return nil, false, err
	}
	if ok, _ := client.Extension("STARTTLS"); !ok {
		return client, encrypted, nil
	}
	if err := client.StartTLS(m.clientTLSConfig()); err != nil {
		client.Close()
		return nil, false, fmt.Errorf("STARTTLS: %w", err)
	}
	return client, true, nil
}

func (m *SMTPMailer) connectPlain(addr string, dialer *net.Dialer) (*smtp.Client, bool, error) {
	// Plain SMTP (port 25): no encryption
	conn, err := dialer.Dial("tcp", addr)
	if err != nil {
		return nil, false, fmt.Errorf("dial: %w", err)
	}
	client, err := smtp.NewClient(conn, m.config.Host)
	if err != nil {
		conn.Close()
		return nil, false, fmt.Errorf("SMTP client: %w", err)
	}
	return client, false, nil
}

func normalizedSMTPProtocol(protocol string) string {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "", "auto":
		return "auto"
	case "tls":
		return "tls"
	case "starttls":
		return "starttls"
	case "none":
		return "none"
	default:
		return strings.ToLower(strings.TrimSpace(protocol))
	}
}

func shouldRetryWithNegotiatedSMTP(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "first record does not look like a tls handshake")
}

func (m *SMTPMailer) clientTLSConfig() *tls.Config {
	if m.tlsConfig != nil {
		cfg := m.tlsConfig.Clone()
		if cfg.ServerName == "" {
			cfg.ServerName = m.config.Host
		}
		return cfg
	}
	return &tls.Config{ServerName: m.config.Host}
}

// authenticate performs SMTP AUTH PLAIN.
func (m *SMTPMailer) authenticate(client *smtp.Client, encrypted bool) error {
	if m.config.Username == "" {
		return nil
	}

	protocol := normalizedSMTPProtocol(m.config.Protocol)
	if !encrypted {
		if protocol == "auto" && !isLocalSMTPHost(m.config.Host) {
			return fmt.Errorf("server does not offer TLS; select None to allow unencrypted SMTP authentication")
		}
		if protocol == "none" && m.logger != nil {
			m.logger.Warn("SMTP AUTH is using an unencrypted connection; credentials will be sent in plaintext",
				"host", m.config.Host,
				"port", m.config.Port,
			)
		}
	}

	auth := smtp.PlainAuth("", m.config.Username, m.config.Password, m.config.Host)
	if !encrypted && protocol == "none" {
		auth = newPlainAuth("", m.config.Username, m.config.Password, m.config.Host, true)
	}
	return client.Auth(auth)
}

type plainAuth struct {
	identity, username, password string
	host                         string
	allowInsecure                bool
}

func newPlainAuth(identity, username, password, host string, allowInsecure bool) smtp.Auth {
	return &plainAuth{
		identity:      identity,
		username:      username,
		password:      password,
		host:          host,
		allowInsecure: allowInsecure,
	}
}

func (a *plainAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if server.Name != a.host {
		return "", nil, errors.New("wrong host name")
	}
	if !server.TLS && !a.allowInsecure && !isLocalSMTPHost(server.Name) {
		return "", nil, errors.New("unencrypted connection")
	}
	resp := []byte(a.identity + "\x00" + a.username + "\x00" + a.password)
	return "PLAIN", resp, nil
}

func (a *plainAuth) Next(_ []byte, more bool) ([]byte, error) {
	if more {
		return nil, errors.New("unexpected server challenge")
	}
	return nil, nil
}

func isLocalSMTPHost(name string) bool {
	return name == "localhost" || name == "127.0.0.1" || name == "::1"
}

// buildMessage constructs an RFC 2822 email message.
func (m *SMTPMailer) buildMessage(subject, body string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("From: %s <%s>\r\n", m.config.FromName, m.config.FromAddr))
	sb.WriteString(fmt.Sprintf("To: %s\r\n", strings.Join(m.config.ToAddrs, ", ")))
	sb.WriteString(fmt.Sprintf("Subject: %s\r\n", subject))
	sb.WriteString("MIME-Version: 1.0\r\n")
	sb.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	sb.WriteString("\r\n")
	sb.WriteString(body)
	return sb.String()
}
