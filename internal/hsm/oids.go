package hsm

import (
	"encoding/asn1"
	"fmt"
)

// HSM Secrets Operator OID namespace
// Using experimental/private range: 1.3.6.1.4.1.99999 (not officially registered)
// In production, this should be replaced with a properly registered enterprise OID

var (
	// Data type OIDs
	OIDPlaintext = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 99999, 1, 1} // Plain text secrets
	OIDJson      = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 99999, 1, 2} // JSON configuration
	OIDPem       = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 99999, 1, 3} // PEM encoded certificates/keys
	OIDBinary    = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 99999, 1, 4} // Binary data
	OIDBase64    = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 99999, 1, 5} // Base64 encoded data
	OIDX509Cert  = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 99999, 1, 6} // X.509 certificate
	OIDPrivKey   = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 99999, 1, 7} // Private key material
	OIDDockerCfg = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 99999, 1, 8} // Docker config JSON
)

// SecretDataType represents the type of data stored in HSM
type SecretDataType string

const (
	DataTypePlaintext SecretDataType = "plaintext"
	DataTypeJson      SecretDataType = "json"
	DataTypePem       SecretDataType = "pem"
	DataTypeBinary    SecretDataType = "binary"
	DataTypeBase64    SecretDataType = "base64"
	DataTypeX509Cert  SecretDataType = "x509-cert"
	DataTypePrivKey   SecretDataType = "private-key"
	DataTypeDockerCfg SecretDataType = "docker-config"
)

// GetOIDForDataType returns the OID for a given data type
func GetOIDForDataType(dataType SecretDataType) (asn1.ObjectIdentifier, error) {
	switch dataType {
	case DataTypePlaintext:
		return OIDPlaintext, nil
	case DataTypeJson:
		return OIDJson, nil
	case DataTypePem:
		return OIDPem, nil
	case DataTypeBinary:
		return OIDBinary, nil
	case DataTypeBase64:
		return OIDBase64, nil
	case DataTypeX509Cert:
		return OIDX509Cert, nil
	case DataTypePrivKey:
		return OIDPrivKey, nil
	case DataTypeDockerCfg:
		return OIDDockerCfg, nil
	default:
		return nil, fmt.Errorf("unknown data type: %s", dataType)
	}
}

// GetDataTypeForOID returns the data type for a given OID
func GetDataTypeForOID(oid asn1.ObjectIdentifier) (SecretDataType, error) {
	switch {
	case oid.Equal(OIDPlaintext):
		return DataTypePlaintext, nil
	case oid.Equal(OIDJson):
		return DataTypeJson, nil
	case oid.Equal(OIDPem):
		return DataTypePem, nil
	case oid.Equal(OIDBinary):
		return DataTypeBinary, nil
	case oid.Equal(OIDBase64):
		return DataTypeBase64, nil
	case oid.Equal(OIDX509Cert):
		return DataTypeX509Cert, nil
	case oid.Equal(OIDPrivKey):
		return DataTypePrivKey, nil
	case oid.Equal(OIDDockerCfg):
		return DataTypeDockerCfg, nil
	default:
		return "", fmt.Errorf("unknown OID: %s", oid.String())
	}
}

// EncodeDER returns the DER encoding of an OID for use in PKCS#11 CKA_OBJECT_ID
func EncodeDER(oid asn1.ObjectIdentifier) ([]byte, error) {
	return asn1.Marshal(oid)
}

// DecodeDER decodes a DER-encoded OID from PKCS#11 CKA_OBJECT_ID
func DecodeDER(der []byte) (asn1.ObjectIdentifier, error) {
	var oid asn1.ObjectIdentifier
	_, err := asn1.Unmarshal(der, &oid)
	return oid, err
}

// InferDataType attempts to infer the data type from content
func InferDataType(data []byte) SecretDataType {
	content := string(data)

	// Check for PEM format
	if len(content) > 20 && content[:5] == "-----" {
		return DataTypePem
	}

	// Check for JSON format
	if len(content) > 0 && (content[0] == '{' || content[0] == '[') {
		return DataTypeJson
	}

	// Check if it's valid base64
	if isValidBase64(content) {
		return DataTypeBase64
	}

	// Check for binary data (contains non-printable chars)
	for _, b := range data {
		if b < 32 && b != 9 && b != 10 && b != 13 { // Allow tab, LF, CR
			return DataTypeBinary
		}
	}

	// Default to plaintext
	return DataTypePlaintext
}

// isValidBase64 checks if a string is valid base64
func isValidBase64(s string) bool {
	if len(s) == 0 {
		return false
	}

	// Base64 strings should be multiple of 4 in length (with padding)
	if len(s)%4 != 0 {
		return false
	}

	// Check for valid base64 characters
	for _, c := range s {
		if (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') &&
			(c < '0' || c > '9') && c != '+' && c != '/' && c != '=' {
			return false
		}
	}

	return true
}
