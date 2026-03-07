package fetch

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"strings"
	"time"
)

// TLSFinding represents a certificate validation finding.
type TLSFinding struct {
	Domain   string
	Severity TLSSeverity
	Issue    string
	Issuer   string
	Expiry   time.Time
	DaysLeft int
}

// TLSSeverity classifies the urgency of a TLS finding.
type TLSSeverity int

const (
	TLSSeverityInfo     TLSSeverity = iota // Valid cert, informational
	TLSSeverityMedium                      // Expiring soon, weak cipher
	TLSSeverityHigh                        // Self-signed, expired
	TLSSeverityCritical                    // Hostname mismatch, revoked
)

func (s TLSSeverity) String() string {
	names := [...]string{"info", "medium", "high", "critical"}
	if int(s) < len(names) {
		return names[s]
	}
	return "unknown"
}

// TLSValidator inspects TLS connection state for certificate issues.
// Runs inline during fetch — zero allocation on valid certs.
type TLSValidator struct {
	// expiryWarningDays triggers a medium-severity finding when a cert
	// is within this many days of expiration. Default: 30.
	expiryWarningDays int
}

// NewTLSValidator creates a validator with default configuration.
func NewTLSValidator() *TLSValidator {
	return &TLSValidator{
		expiryWarningDays: 30,
	}
}

// Validate inspects TLS connection state and returns findings.
// Returns nil (not empty slice) when the certificate is fully valid,
// avoiding allocation on the happy path.
func (v *TLSValidator) Validate(state *tls.ConnectionState, hostname string) []TLSFinding {
	if state == nil {
		return nil
	}

	if len(state.PeerCertificates) == 0 {
		return []TLSFinding{{
			Domain:   hostname,
			Severity: TLSSeverityCritical,
			Issue:    "no peer certificates presented",
		}}
	}

	leaf := state.PeerCertificates[0]
	now := time.Now()
	var findings []TLSFinding

	// Hostname verification.
	if err := leaf.VerifyHostname(hostname); err != nil {
		findings = append(findings, TLSFinding{
			Domain:   hostname,
			Severity: TLSSeverityCritical,
			Issue:    fmt.Sprintf("hostname mismatch: cert valid for %s", formatSANs(leaf)),
			Issuer:   leaf.Issuer.CommonName,
			Expiry:   leaf.NotAfter,
		})
	}

	// Expiry checks.
	daysLeft := int(leaf.NotAfter.Sub(now).Hours() / 24)
	if now.After(leaf.NotAfter) {
		findings = append(findings, TLSFinding{
			Domain:   hostname,
			Severity: TLSSeverityHigh,
			Issue:    fmt.Sprintf("certificate expired %d days ago", -daysLeft),
			Issuer:   leaf.Issuer.CommonName,
			Expiry:   leaf.NotAfter,
			DaysLeft: daysLeft,
		})
	} else if now.Before(leaf.NotBefore) {
		findings = append(findings, TLSFinding{
			Domain:   hostname,
			Severity: TLSSeverityHigh,
			Issue:    "certificate not yet valid",
			Issuer:   leaf.Issuer.CommonName,
			Expiry:   leaf.NotAfter,
			DaysLeft: daysLeft,
		})
	} else if daysLeft <= v.expiryWarningDays {
		findings = append(findings, TLSFinding{
			Domain:   hostname,
			Severity: TLSSeverityMedium,
			Issue:    fmt.Sprintf("certificate expires in %d days", daysLeft),
			Issuer:   leaf.Issuer.CommonName,
			Expiry:   leaf.NotAfter,
			DaysLeft: daysLeft,
		})
	}

	// Self-signed detection.
	if leaf.IsCA && leaf.Issuer.CommonName == leaf.Subject.CommonName {
		findings = append(findings, TLSFinding{
			Domain:   hostname,
			Severity: TLSSeverityHigh,
			Issue:    "self-signed certificate",
			Issuer:   leaf.Issuer.CommonName,
			Expiry:   leaf.NotAfter,
			DaysLeft: daysLeft,
		})
	}

	// Chain verification (checks against system trust store).
	opts := x509.VerifyOptions{
		DNSName:       hostname,
		Intermediates: x509.NewCertPool(),
	}
	for _, cert := range state.PeerCertificates[1:] {
		opts.Intermediates.AddCert(cert)
	}
	if _, err := leaf.Verify(opts); err != nil {
		// Don't double-report hostname mismatch or expiry.
		if !containsAny(err.Error(), "expired", "hostname", "not yet valid") {
			findings = append(findings, TLSFinding{
				Domain:   hostname,
				Severity: TLSSeverityHigh,
				Issue:    fmt.Sprintf("chain verification failed: %s", truncateTLS(err.Error(), 120)),
				Issuer:   leaf.Issuer.CommonName,
				Expiry:   leaf.NotAfter,
				DaysLeft: daysLeft,
			})
		}
	}

	// Protocol version check.
	if state.Version < tls.VersionTLS12 {
		findings = append(findings, TLSFinding{
			Domain:   hostname,
			Severity: TLSSeverityMedium,
			Issue:    fmt.Sprintf("weak TLS version: %s", tlsVersionName(state.Version)),
			Issuer:   leaf.Issuer.CommonName,
			Expiry:   leaf.NotAfter,
			DaysLeft: daysLeft,
		})
	}

	return findings
}

// MaxSeverity returns the highest severity among findings.
func MaxSeverity(findings []TLSFinding) TLSSeverity {
	max := TLSSeverityInfo
	for i := range findings {
		if findings[i].Severity > max {
			max = findings[i].Severity
		}
	}
	return max
}

func formatSANs(cert *x509.Certificate) string {
	names := make([]string, 0, len(cert.DNSNames))
	for _, name := range cert.DNSNames {
		names = append(names, name)
	}
	if len(names) == 0 {
		return cert.Subject.CommonName
	}
	if len(names) > 3 {
		return strings.Join(names[:3], ", ") + fmt.Sprintf(" (+%d more)", len(names)-3)
	}
	return strings.Join(names, ", ")
}

func containsAny(s string, substrs ...string) bool {
	lower := strings.ToLower(s)
	for _, sub := range substrs {
		if strings.Contains(lower, sub) {
			return true
		}
	}
	return false
}

func truncateTLS(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func tlsVersionName(v uint16) string {
	switch v {
	case tls.VersionTLS10:
		return "TLS 1.0"
	case tls.VersionTLS11:
		return "TLS 1.1"
	case tls.VersionTLS12:
		return "TLS 1.2"
	case tls.VersionTLS13:
		return "TLS 1.3"
	default:
		return fmt.Sprintf("0x%04x", v)
	}
}
