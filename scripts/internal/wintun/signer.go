package wintun

import (
	"bytes"
	"crypto/x509"
	"crypto/x509/pkix"
	encasn1 "encoding/asn1"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strings"

	"debug/pe"

	"golang.org/x/crypto/cryptobyte"
	"golang.org/x/crypto/cryptobyte/asn1"
)

// SignerSubject returns the subject of the Authenticode signer's certificate in the PE file
// path, in the form Windows shows it (X509Certificate2.Subject): "CN=…, O=…, L=…, S=…,
// C=…". The signer is the first one in the primary signature, the one
// Get-AuthenticodeSignature names too.
//
// Here the signature is only parsed, not verified: WinVerifyTrust decides whether it is
// valid (sign_windows.go).
func SignerSubject(path string) (string, error) {
	cert, err := signerCertificate(path)
	if err != nil {
		return "", err
	}
	return x500String(cert.RawSubject)
}

var oidSignedData = encasn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}

func signerCertificate(path string) (*x509.Certificate, error) {
	f, err := pe.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var dir pe.DataDirectory
	switch h := f.OptionalHeader.(type) {
	case *pe.OptionalHeader32:
		dir = h.DataDirectory[pe.IMAGE_DIRECTORY_ENTRY_SECURITY]
	case *pe.OptionalHeader64:
		dir = h.DataDirectory[pe.IMAGE_DIRECTORY_ENTRY_SECURITY]
	default:
		return nil, errors.New("not a PE file")
	}
	if dir.Size < 8 {
		return nil, errors.New("no signature")
	}

	// The address of the security directory is a file offset, not an image offset.
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if uint64(dir.VirtualAddress)+uint64(dir.Size) > uint64(len(raw)) {
		return nil, errors.New("signature directory beyond the end of the file")
	}
	// WIN_CERTIFICATE: length, revision, type (2 is PKCS#7 SignedData), data.
	wc := raw[dir.VirtualAddress : dir.VirtualAddress+dir.Size]
	length := binary.LittleEndian.Uint32(wc)
	if typ := binary.LittleEndian.Uint16(wc[6:]); typ != 2 || length < 8 || length > dir.Size {
		return nil, fmt.Errorf("signature is not PKCS#7 (type %d)", typ)
	}
	return parseSignedData(wc[8:length])
}

// parseSignedData finds the certificate of the first signer in PKCS#7 SignedData.
func parseSignedData(der []byte) (*x509.Certificate, error) {
	bad := errors.New("signature: invalid PKCS#7 structure")
	in := cryptobyte.String(der)
	var ci, content, sd cryptobyte.String
	var oid encasn1.ObjectIdentifier
	if !in.ReadASN1(&ci, asn1.SEQUENCE) || !ci.ReadASN1ObjectIdentifier(&oid) {
		return nil, bad
	}
	if !oid.Equal(oidSignedData) {
		return nil, fmt.Errorf("signature: type %v, not SignedData", oid)
	}
	var version int
	var certs cryptobyte.String
	var hasCerts bool
	var signers, si cryptobyte.String
	if !ci.ReadASN1(&content, asn1.Tag(0).Constructed().ContextSpecific()) ||
		!content.ReadASN1(&sd, asn1.SEQUENCE) ||
		!sd.ReadASN1Integer(&version) ||
		!sd.SkipASN1(asn1.SET) || // digestAlgorithms
		!sd.SkipASN1(asn1.SEQUENCE) || // encapContentInfo
		!sd.ReadOptionalASN1(&certs, &hasCerts, asn1.Tag(0).Constructed().ContextSpecific()) ||
		!sd.SkipOptionalASN1(asn1.Tag(1).Constructed().ContextSpecific()) ||
		!sd.ReadASN1(&signers, asn1.SET) ||
		!signers.ReadASN1(&si, asn1.SEQUENCE) ||
		!si.ReadASN1Integer(&version) {
		return nil, bad
	}

	// The signer is named by issuer and serial number (version 1) or by key identifier
	// (version 3).
	var issuer cryptobyte.String
	serial := new(big.Int)
	var keyID []byte
	switch version {
	case 1:
		var ias cryptobyte.String
		if !si.ReadASN1(&ias, asn1.SEQUENCE) || !ias.ReadASN1Element(&issuer, asn1.SEQUENCE) || !ias.ReadASN1Integer(serial) {
			return nil, bad
		}
	case 3:
		var id cryptobyte.String
		if !si.ReadASN1(&id, asn1.Tag(0).ContextSpecific()) {
			return nil, bad
		}
		keyID = id
	default:
		return nil, fmt.Errorf("signature: SignerInfo version %d", version)
	}

	for !certs.Empty() {
		var c cryptobyte.String
		if !certs.ReadASN1Element(&c, asn1.SEQUENCE) {
			return nil, bad
		}
		cert, err := x509.ParseCertificate(c)
		if err != nil {
			continue
		}
		if keyID != nil && bytes.Equal(cert.SubjectKeyId, keyID) ||
			keyID == nil && bytes.Equal(cert.RawIssuer, issuer) && cert.SerialNumber.Cmp(serial) == 0 {
			return cert, nil
		}
	}
	return nil, errors.New("signature: it has no signer certificate")
}

// attrNames are the attribute names as Windows writes them (CertNameToStr).
var attrNames = map[string]string{
	"2.5.4.3":                    "CN",
	"2.5.4.4":                    "SN",
	"2.5.4.5":                    "SERIALNUMBER",
	"2.5.4.6":                    "C",
	"2.5.4.7":                    "L",
	"2.5.4.8":                    "S",
	"2.5.4.9":                    "STREET",
	"2.5.4.10":                   "O",
	"2.5.4.11":                   "OU",
	"2.5.4.12":                   "T",
	"2.5.4.17":                   "PostalCode",
	"2.5.4.42":                   "G",
	"2.5.4.43":                   "I",
	"1.2.840.113549.1.9.1":       "E",
	"0.9.2342.19200300.100.1.25": "DC",
}

// x500String writes the name the same way Windows does: from the last RDN to the first,
// separated by ", ", the attributes of one RDN by " + "; a value with special characters
// in quotes.
func x500String(rawName []byte) (string, error) {
	var rdns pkix.RDNSequence
	if rest, err := encasn1.Unmarshal(rawName, &rdns); err != nil || len(rest) > 0 {
		return "", fmt.Errorf("name in the certificate: %v", err)
	}
	var parts []string
	for i := len(rdns) - 1; i >= 0; i-- {
		var attrs []string
		for _, a := range rdns[i] {
			name, ok := attrNames[a.Type.String()]
			if !ok {
				name = "OID." + a.Type.String()
			}
			attrs = append(attrs, name+"="+quoteValue(fmt.Sprint(a.Value)))
		}
		parts = append(parts, strings.Join(attrs, " + "))
	}
	return strings.Join(parts, ", "), nil
}

func quoteValue(v string) string {
	if v == "" || strings.ContainsAny(v, ",+=\"\n<>#;") || strings.TrimSpace(v) != v {
		return `"` + strings.ReplaceAll(v, `"`, `""`) + `"`
	}
	return v
}
