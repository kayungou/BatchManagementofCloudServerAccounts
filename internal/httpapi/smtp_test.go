package httpapi

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"
)

func TestSMTPTransportForUsesImplicitTLSOnPort465(t *testing.T) {
	tests := []struct {
		name         string
		port         int
		startTLS     bool
		wantImplicit bool
		wantStartTLS bool
	}{
		{name: "implicit TLS on 465", port: 465, startTLS: true, wantImplicit: true},
		{name: "465 remains implicit when legacy flag is off", port: 465, startTLS: false, wantImplicit: true},
		{name: "STARTTLS on submission port", port: 587, startTLS: true, wantStartTLS: true},
		{name: "plain SMTP when disabled", port: 25, startTLS: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transport := smtpTransportFor(smtpSetting{Host: "smtp.example.com", Port: test.port, StartTLS: test.startTLS})
			if transport.implicitTLS != test.wantImplicit || transport.startTLS != test.wantStartTLS {
				t.Fatalf("transport modes = (implicit=%v, starttls=%v), want (%v, %v)", transport.implicitTLS, transport.startTLS, test.wantImplicit, test.wantStartTLS)
			}
		})
	}
}

func TestDeliverSMTPOverImplicitTLS(t *testing.T) {
	certificate, roots := testSMTPCertificate(t)
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	received := make(chan string, 1)
	serverErr := make(chan error, 1)
	go serveTestSMTP(listener, received, serverErr)

	setting := smtpSetting{Host: "localhost", From: "sender@example.com"}
	transport := smtpTransport{
		address:        listener.Addr().String(),
		host:           setting.Host,
		implicitTLS:    true,
		tlsConfig:      &tls.Config{ServerName: "localhost", RootCAs: roots, MinVersion: tls.VersionTLS12},
		connectTimeout: time.Second,
		ioTimeout:      time.Second,
	}
	message := []byte("Subject: SMTP test\r\n\r\nimplicit TLS works")
	if err := deliverSMTP(context.Background(), transport, setting, "never-log-this-password", "recipient@example.com", message); err != nil {
		t.Fatalf("deliver SMTP: %v", err)
	}
	select {
	case err := <-serverErr:
		t.Fatal(err)
	case got := <-received:
		if !strings.Contains(got, "implicit TLS works") {
			t.Fatalf("message body = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("SMTP server did not receive the message")
	}
}

func TestDeliverSMTPGreetingTimeoutHasSafeStageError(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr == nil {
			accepted <- conn
		}
	}()

	setting := smtpSetting{Host: "localhost", From: "sender@example.com", Username: "smtp-user"}
	transport := smtpTransport{
		address:        listener.Addr().String(),
		host:           setting.Host,
		tlsConfig:      &tls.Config{ServerName: "localhost", MinVersion: tls.VersionTLS12},
		connectTimeout: time.Second,
		ioTimeout:      50 * time.Millisecond,
	}
	const password = "never-log-this-password"
	err = deliverSMTP(context.Background(), transport, setting, password, "recipient@example.com", []byte("test"))
	if conn := <-accepted; conn != nil {
		_ = conn.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "SMTP 服务问候失败") {
		t.Fatalf("error = %v, want staged greeting timeout", err)
	}
	if strings.Contains(err.Error(), password) {
		t.Fatal("SMTP error exposed the password")
	}
}

func serveTestSMTP(listener net.Listener, received chan<- string, result chan<- error) {
	conn, err := listener.Accept()
	if err != nil {
		result <- err
		return
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	if _, err := writer.WriteString("220 localhost ESMTP\r\n"); err != nil {
		result <- err
		return
	}
	if err := writer.Flush(); err != nil {
		result <- err
		return
	}
	var message strings.Builder
	inData := false
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			result <- err
			return
		}
		trimmed := strings.TrimRight(line, "\r\n")
		if inData {
			if trimmed == "." {
				inData = false
				if _, err := writer.WriteString("250 queued\r\n"); err != nil {
					result <- err
					return
				}
				if err := writer.Flush(); err != nil {
					result <- err
					return
				}
				received <- message.String()
				return
			}
			message.WriteString(line)
			continue
		}
		var response string
		switch {
		case strings.HasPrefix(trimmed, "EHLO "), strings.HasPrefix(trimmed, "HELO "):
			response = "250 localhost\r\n"
		case strings.HasPrefix(trimmed, "MAIL FROM:"), strings.HasPrefix(trimmed, "RCPT TO:"):
			response = "250 accepted\r\n"
		case trimmed == "DATA":
			response = "354 send data\r\n"
			inData = true
		default:
			response = "500 unexpected command\r\n"
		}
		if _, err := writer.WriteString(response); err != nil {
			result <- err
			return
		}
		if err := writer.Flush(); err != nil {
			result <- err
			return
		}
	}
}

func testSMTPCertificate(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "localhost"},
		DNSNames:              []string{"localhost"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	privateKeyDER, err := x509.MarshalECPrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER})
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privateKeyDER})
	certificate, err := tls.X509KeyPair(certificatePEM, privateKeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	parsedCertificate, err := x509.ParseCertificate(certificateDER)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(parsedCertificate)
	return certificate, roots
}
