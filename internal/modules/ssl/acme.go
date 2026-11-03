package ssl

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/acme"

	"github.com/icebays/al1add/internal/cfapi"
)

func rsaKey(bits int) (crypto.Signer, error) {
	return rsa.GenerateKey(randReader(), bits)
}

// accountKey loads or creates the shared ACME account key.
func (m *Module) accountKey() (crypto.Signer, error) {
	if m.cfg.ACMEKeyPEM != "" {
		block, _ := pem.Decode([]byte(m.cfg.ACMEKeyPEM))
		if block != nil {
			if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
				if s, ok := k.(crypto.Signer); ok {
					return s, nil
				}
			}
		}
	}
	k, err := genKey("ec256")
	if err != nil {
		return nil, err
	}
	pemStr, err := encodeKey(k)
	if err != nil {
		return nil, err
	}
	m.cfg.ACMEKeyPEM = pemStr
	return k, nil
}

// issue obtains a certificate for c using ACME.
func (m *Module) issue(ctx context.Context, c *Cert) error {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()

	dir := CADirectories[c.CA]
	if dir == "" {
		return fmt.Errorf("unknown CA %q", c.CA)
	}
	akey, err := m.accountKey()
	if err != nil {
		return err
	}
	client := &acme.Client{Key: akey, DirectoryURL: dir}

	acct := &acme.Account{}
	if c.Email != "" {
		acct.Contact = []string{"mailto:" + c.Email}
	}
	if _, err := client.Register(ctx, acct, acme.AcceptTOS); err != nil && err != acme.ErrAccountAlreadyExists {
		return fmt.Errorf("acme register: %w", err)
	}

	var authzIDs []acme.AuthzID
	for _, d := range c.Domains {
		authzIDs = append(authzIDs, acme.AuthzID{Type: "dns", Value: d})
	}
	order, err := client.AuthorizeOrder(ctx, authzIDs)
	if err != nil {
		return fmt.Errorf("acme order: %w", err)
	}

	var cf *cfSolver
	if c.Challenge == "dns-01" {
		cf = m.newCFSolver(c.CFToken)
		defer cf.cleanup(context.Background())
	}

	for _, authzURL := range order.AuthzURLs {
		authz, err := client.GetAuthorization(ctx, authzURL)
		if err != nil {
			return err
		}
		if authz.Status == acme.StatusValid {
			continue
		}
		var chal *acme.Challenge
		want := c.Challenge
		for _, ch := range authz.Challenges {
			if ch.Type == want {
				chal = ch
				break
			}
		}
		if chal == nil {
			return fmt.Errorf("domain %s: no %s challenge offered", authz.Identifier.Value, want)
		}
		switch want {
		case "http-01":
			resp, err := client.HTTP01ChallengeResponse(chal.Token)
			if err != nil {
				return err
			}
			m.mu.Lock()
			m.httpToks[chal.Token] = resp
			m.mu.Unlock()
			defer func(tok string) {
				m.mu.Lock()
				delete(m.httpToks, tok)
				m.mu.Unlock()
			}(chal.Token)
		case "dns-01":
			rec, err := client.DNS01ChallengeRecord(chal.Token)
			if err != nil {
				return err
			}
			if err := cf.present(ctx, authz.Identifier.Value, rec); err != nil {
				return fmt.Errorf("cloudflare dns-01: %w", err)
			}
		default:
			return fmt.Errorf("unsupported challenge %q", want)
		}
		if _, err := client.Accept(ctx, chal); err != nil {
			return fmt.Errorf("accept challenge: %w", err)
		}
		if _, err := client.WaitAuthorization(ctx, authzURL); err != nil {
			return fmt.Errorf("authorization %s failed: %w", authz.Identifier.Value, err)
		}
	}

	certKey, err := genKey(c.KeyType)
	if err != nil {
		return err
	}
	csr, err := x509.CreateCertificateRequest(randReader(), &x509.CertificateRequest{
		DNSNames: c.Domains,
	}, certKey)
	if err != nil {
		return err
	}
	ders, _, err := client.CreateOrderCert(ctx, order.FinalizeURL, csr, true)
	if err != nil {
		return fmt.Errorf("finalize: %w", err)
	}
	var chain strings.Builder
	for _, der := range ders {
		pem.Encode(&chain, &pem.Block{Type: "CERTIFICATE", Bytes: der})
	}
	keyPEM, err := encodeKey(certKey)
	if err != nil {
		return err
	}
	c.CertPEM = chain.String()
	c.KeyPEM = keyPEM
	c.LastError = ""
	c.LastRenewal = time.Now().Format("2006-01-02 15:04:05")
	if leaf, err := x509.ParseCertificate(ders[0]); err == nil {
		c.NotAfter = leaf.NotAfter.Format("2006-01-02 15:04:05")
		c.NotBefore = leaf.NotBefore.Format("2006-01-02 15:04:05")
		c.Issuer = leaf.Issuer.CommonName
	}
	m.log.Infof("issued certificate %q for %s (expires %s)", c.Name, strings.Join(c.Domains, ", "), c.NotAfter)
	return nil
}

// cfSolver presents dns-01 challenges via Cloudflare and cleans them up.
type cfSolver struct {
	client  *cfapi.Client
	created map[string]string // fqdn -> recordID (zoneID in key prefix)
}

func (s *cfSolver) present(ctx context.Context, domain, value string) error {
	domain = strings.TrimPrefix(domain, "*.")
	zone, err := s.findZone(ctx, domain)
	if err != nil {
		return err
	}
	name := "_acme-challenge." + domain
	rec, err := s.client.CreateRecord(ctx, zone.ID, cfapi.DNSRecord{
		Type: "TXT", Name: name, Content: value, TTL: 60, Comment: "al1add acme",
	})
	if err != nil {
		return err
	}
	s.created[zone.ID+"|"+rec.ID] = name
	// Give Cloudflare a moment to propagate.
	time.Sleep(12 * time.Second)
	return nil
}

func (s *cfSolver) findZone(ctx context.Context, domain string) (*cfapi.Zone, error) {
	labels := strings.Split(domain, ".")
	for i := 0; i < len(labels)-1; i++ {
		cand := strings.Join(labels[i:], ".")
		z, err := s.client.ZoneByName(ctx, cand)
		if err == nil {
			return z, nil
		}
		if !cfapi.IsNotFound(err) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("no Cloudflare zone found for %s", domain)
}

func (s *cfSolver) cleanup(ctx context.Context) {
	for key := range s.created {
		zoneID, recID, _ := strings.Cut(key, "|")
		_ = s.client.DeleteRecord(ctx, zoneID, recID)
	}
}
