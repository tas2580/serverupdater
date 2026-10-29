package identity

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	"serverupdater/internal/files"
)

type Config struct {
	Listen               string `json:"listen"`
	DataDir              string `json:"data_dir"`
	Certificate          string `json:"certificate"`
	PrivateKey           string `json:"private_key"`
	TokenHash            string `json:"token_hash"`
	CheckIntervalSeconds int    `json:"check_interval_seconds"`
}

func RandomID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func NewToken() (string, string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	h := sha256.Sum256([]byte(token))
	return token, hex.EncodeToString(h[:]), nil
}

func VerifyToken(token, expected string) bool {
	h := sha256.Sum256([]byte(token))
	want, err := hex.DecodeString(expected)
	return err == nil && len(want) == 32 && subtle.ConstantTimeCompare(h[:], want) == 1
}

func NormalizeFingerprint(s string) (string, error) {
	s = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), ":", ""))
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 32 {
		return "", errors.New("Zertifikatsfingerabdruck muss 64 Hex-Zeichen enthalten")
	}
	return s, nil
}

func Fingerprint(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	p, _ := pem.Decode(b)
	if p == nil || p.Type != "CERTIFICATE" {
		return "", errors.New("ungültiges Zertifikat")
	}
	if _, err = x509.ParseCertificate(p.Bytes); err != nil {
		return "", err
	}
	h := sha256.Sum256(p.Bytes)
	return hex.EncodeToString(h[:]), nil
}

func Load(path string) (Config, error) {
	var c Config
	if err := files.Read(path, &c); err != nil {
		return c, err
	}
	if c.Listen == "" || !filepath.IsAbs(c.DataDir) || !filepath.IsAbs(c.Certificate) || !filepath.IsAbs(c.PrivateKey) {
		return c, errors.New("Listen-Adresse und absolute Pfade für Daten und TLS sind erforderlich")
	}
	h, err := hex.DecodeString(c.TokenHash)
	if err != nil || len(h) != 32 {
		return c, errors.New("ungültiger Token-Hash")
	}
	if c.CheckIntervalSeconds < 60 {
		return c, errors.New("Prüfintervall muss mindestens 60 Sekunden betragen")
	}
	return c, nil
}

func Init(path, dataDir, listen string, interval int) (Config, string, error) {
	var c Config
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		return c, "", errors.New("Konfiguration existiert bereits oder ist nicht zugänglich")
	}
	if !filepath.IsAbs(path) || !filepath.IsAbs(dataDir) || listen == "" || interval < 60 {
		return c, "", errors.New("absolute Pfade, Listen-Adresse und Intervall >= 60 erforderlich")
	}
	token, hash, err := NewToken()
	if err != nil {
		return c, "", err
	}
	c = Config{Listen: listen, DataDir: dataDir, Certificate: filepath.Join(filepath.Dir(path), "server.crt"), PrivateKey: filepath.Join(filepath.Dir(path), "server.key"), TokenHash: hash, CheckIntervalSeconds: interval}
	// Never replace existing identity files, including remnants of an interrupted init.
	for _, p := range []string{c.Certificate, c.PrivateKey} {
		if _, err := os.Lstat(p); !errors.Is(err, os.ErrNotExist) {
			return Config{}, "", fmt.Errorf("TLS-Datei existiert bereits oder ist nicht zugänglich: %s", p)
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return c, "", err
	}
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return c, "", err
	}
	now := time.Now()
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "ServerUpdater"}, NotBefore: now.Add(-time.Hour), NotAfter: now.AddDate(10, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, DNSNames: []string{"localhost"}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return c, "", err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return c, "", err
	}
	if err = files.WriteBytes(c.PrivateKey, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0600); err != nil {
		return c, "", err
	}
	if err = files.WriteBytes(c.Certificate, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		return c, "", err
	}
	if err = os.MkdirAll(dataDir, 0700); err != nil {
		return c, "", err
	}
	if err = files.Write(path, c); err != nil {
		return c, "", err
	}
	return c, token, nil
}
