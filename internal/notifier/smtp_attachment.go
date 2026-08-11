package notifier

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"

	providersmtp "github.com/projectdiscovery/notify/pkg/providers/smtp"
	"github.com/projectdiscovery/notify/pkg/utils"
)

type smtpAttachmentSender struct {
	option  *providersmtp.Options
	timeout time.Duration
	counter int
}

func newSMTPAttachmentSender(option *providersmtp.Options) *smtpAttachmentSender {
	return &smtpAttachmentSender{option: option, timeout: 30 * time.Second}
}

func (s *smtpAttachmentSender) SendAttachment(ctx context.Context, message string, attachment Attachment) error {
	s.counter++
	message = utils.FormatMessage(message, utils.SelectFormat("", s.option.SMTPFormat), s.counter)
	from, err := mail.ParseAddress(s.option.FromAddress)
	if err != nil {
		return fmt.Errorf("parse SMTP from address: %w", err)
	}
	recipients := make([]string, 0, len(s.option.SMTPCC))
	for _, raw := range s.option.SMTPCC {
		address, err := mail.ParseAddress(raw)
		if err != nil {
			return fmt.Errorf("parse SMTP recipient: %w", err)
		}
		recipients = append(recipients, address.Address)
	}
	payload, err := buildMIMEMessage(from.Address, recipients, s.option.Subject, message, s.option.HTML, attachment)
	if err != nil {
		return err
	}
	serverAddress, host, err := smtpServerAddress(s.option.Server)
	if err != nil {
		return err
	}
	dialer := net.Dialer{Timeout: s.timeout}
	connection, err := dialer.DialContext(ctx, "tcp", serverAddress)
	if err != nil {
		return fmt.Errorf("connect to SMTP server: %w", err)
	}
	defer connection.Close()
	stopCancellation := context.AfterFunc(ctx, func() { _ = connection.SetDeadline(time.Now()) })
	defer stopCancellation()
	deadline := time.Now().Add(s.timeout)
	if value, ok := ctx.Deadline(); ok && value.Before(deadline) {
		deadline = value
	}
	_ = connection.SetDeadline(deadline)
	client, err := smtp.NewClient(connection, host)
	if err != nil {
		return fmt.Errorf("start SMTP client: %w", err)
	}
	defer client.Close()
	if !s.option.DisableStartTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return fmt.Errorf("SMTP server does not support STARTTLS")
		}
		if err := client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("start SMTP TLS: %w", err)
		}
	}
	if s.option.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", s.option.Username, s.option.Password, host)); err != nil {
			return fmt.Errorf("authenticate to SMTP server: %w", err)
		}
	}
	if err := client.Mail(from.Address); err != nil {
		return fmt.Errorf("set SMTP sender: %w", err)
	}
	for _, recipient := range recipients {
		if err := client.Rcpt(recipient); err != nil {
			return fmt.Errorf("set SMTP recipient: %w", err)
		}
	}
	data, err := client.Data()
	if err != nil {
		return fmt.Errorf("open SMTP message body: %w", err)
	}
	if _, err := data.Write(payload); err != nil {
		_ = data.Close()
		return fmt.Errorf("write SMTP message: %w", err)
	}
	if err := data.Close(); err != nil {
		return fmt.Errorf("finish SMTP message: %w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("finish SMTP session: %w", err)
	}
	return nil
}

func buildMIMEMessage(from string, recipients []string, subject, message string, html bool, attachment Attachment) ([]byte, error) {
	if strings.ContainsAny(subject, "\r\n") {
		return nil, fmt.Errorf("SMTP subject must not contain newlines")
	}
	boundary := "fetchdiff-mixed-boundary"
	bodyType := "text/plain; charset=utf-8"
	if html {
		bodyType = "text/html; charset=utf-8"
	}
	var output bytes.Buffer
	fmt.Fprintf(&output, "From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=%q\r\n\r\n", from, strings.Join(recipients, ", "), mime.QEncoding.Encode("utf-8", subject), boundary)
	fmt.Fprintf(&output, "--%s\r\nContent-Type: %s\r\nContent-Transfer-Encoding: base64\r\n\r\n%s\r\n", boundary, bodyType, encodeBase64([]byte(message)))
	fmt.Fprintf(&output, "--%s\r\nContent-Type: %s\r\nContent-Disposition: attachment; filename=%q\r\nContent-Transfer-Encoding: base64\r\n\r\n%s\r\n--%s--\r\n", boundary, attachment.ContentType, safeAttachmentName(attachment.Name), encodeBase64(attachment.Data), boundary)
	return output.Bytes(), nil
}

func encodeBase64(value []byte) string {
	encoded := base64.StdEncoding.EncodeToString(value)
	var output strings.Builder
	for len(encoded) > 76 {
		output.WriteString(encoded[:76])
		output.WriteString("\r\n")
		encoded = encoded[76:]
	}
	output.WriteString(encoded)
	return output.String()
}

func smtpServerAddress(raw string) (string, string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", "", fmt.Errorf("SMTP server is empty")
	}
	if host, port, err := net.SplitHostPort(value); err == nil {
		return net.JoinHostPort(host, port), host, nil
	}
	if strings.Contains(value, ":") {
		return "", "", fmt.Errorf("invalid SMTP server address %q", value)
	}
	return net.JoinHostPort(value, "25"), value, nil
}
