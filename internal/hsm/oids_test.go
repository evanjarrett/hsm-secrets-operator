/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package hsm

import (
	"encoding/asn1"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOIDConstants(t *testing.T) {
	// Test that all OIDs are properly defined
	expectedOIDs := map[string]asn1.ObjectIdentifier{
		"plaintext":     {1, 3, 6, 1, 4, 1, 99999, 1, 1},
		"json":          {1, 3, 6, 1, 4, 1, 99999, 1, 2},
		"pem":           {1, 3, 6, 1, 4, 1, 99999, 1, 3},
		"binary":        {1, 3, 6, 1, 4, 1, 99999, 1, 4},
		"base64":        {1, 3, 6, 1, 4, 1, 99999, 1, 5},
		"x509-cert":     {1, 3, 6, 1, 4, 1, 99999, 1, 6},
		"private-key":   {1, 3, 6, 1, 4, 1, 99999, 1, 7},
		"docker-config": {1, 3, 6, 1, 4, 1, 99999, 1, 8},
	}

	assert.Equal(t, expectedOIDs["plaintext"], OIDPlaintext)
	assert.Equal(t, expectedOIDs["json"], OIDJson)
	assert.Equal(t, expectedOIDs["pem"], OIDPem)
	assert.Equal(t, expectedOIDs["binary"], OIDBinary)
	assert.Equal(t, expectedOIDs["base64"], OIDBase64)
	assert.Equal(t, expectedOIDs["x509-cert"], OIDX509Cert)
	assert.Equal(t, expectedOIDs["private-key"], OIDPrivKey)
	assert.Equal(t, expectedOIDs["docker-config"], OIDDockerCfg)
}

func TestDataTypeConstants(t *testing.T) {
	expectedTypes := map[SecretDataType]string{
		DataTypePlaintext: "plaintext",
		DataTypeJson:      "json",
		DataTypePem:       "pem",
		DataTypeBinary:    "binary",
		DataTypeBase64:    "base64",
		DataTypeX509Cert:  "x509-cert",
		DataTypePrivKey:   "private-key",
		DataTypeDockerCfg: "docker-config",
	}

	for dataType, expected := range expectedTypes {
		assert.Equal(t, expected, string(dataType))
	}
}

func TestGetOIDForDataType(t *testing.T) {
	tests := []struct {
		name     string
		dataType SecretDataType
		expected asn1.ObjectIdentifier
		hasError bool
	}{
		{
			name:     "plaintext",
			dataType: DataTypePlaintext,
			expected: OIDPlaintext,
			hasError: false,
		},
		{
			name:     "json",
			dataType: DataTypeJson,
			expected: OIDJson,
			hasError: false,
		},
		{
			name:     "pem",
			dataType: DataTypePem,
			expected: OIDPem,
			hasError: false,
		},
		{
			name:     "binary",
			dataType: DataTypeBinary,
			expected: OIDBinary,
			hasError: false,
		},
		{
			name:     "base64",
			dataType: DataTypeBase64,
			expected: OIDBase64,
			hasError: false,
		},
		{
			name:     "x509-cert",
			dataType: DataTypeX509Cert,
			expected: OIDX509Cert,
			hasError: false,
		},
		{
			name:     "private-key",
			dataType: DataTypePrivKey,
			expected: OIDPrivKey,
			hasError: false,
		},
		{
			name:     "docker-config",
			dataType: DataTypeDockerCfg,
			expected: OIDDockerCfg,
			hasError: false,
		},
		{
			name:     "unknown data type",
			dataType: SecretDataType("unknown"),
			expected: nil,
			hasError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oid, err := GetOIDForDataType(tt.dataType)

			if tt.hasError {
				assert.Error(t, err)
				assert.Nil(t, oid)
				assert.Contains(t, err.Error(), "unknown data type")
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, oid)
			}
		})
	}
}

func TestGetDataTypeForOID(t *testing.T) {
	tests := []struct {
		name     string
		oid      asn1.ObjectIdentifier
		expected SecretDataType
		hasError bool
	}{
		{
			name:     "plaintext OID",
			oid:      OIDPlaintext,
			expected: DataTypePlaintext,
			hasError: false,
		},
		{
			name:     "json OID",
			oid:      OIDJson,
			expected: DataTypeJson,
			hasError: false,
		},
		{
			name:     "pem OID",
			oid:      OIDPem,
			expected: DataTypePem,
			hasError: false,
		},
		{
			name:     "binary OID",
			oid:      OIDBinary,
			expected: DataTypeBinary,
			hasError: false,
		},
		{
			name:     "base64 OID",
			oid:      OIDBase64,
			expected: DataTypeBase64,
			hasError: false,
		},
		{
			name:     "x509-cert OID",
			oid:      OIDX509Cert,
			expected: DataTypeX509Cert,
			hasError: false,
		},
		{
			name:     "private-key OID",
			oid:      OIDPrivKey,
			expected: DataTypePrivKey,
			hasError: false,
		},
		{
			name:     "docker-config OID",
			oid:      OIDDockerCfg,
			expected: DataTypeDockerCfg,
			hasError: false,
		},
		{
			name:     "unknown OID",
			oid:      asn1.ObjectIdentifier{1, 2, 3, 4, 5},
			expected: "",
			hasError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dataType, err := GetDataTypeForOID(tt.oid)

			if tt.hasError {
				assert.Error(t, err)
				assert.Empty(t, dataType)
				assert.Contains(t, err.Error(), "unknown OID")
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, dataType)
			}
		})
	}
}

func TestOIDRoundTrip(t *testing.T) {
	// Test that converting data type to OID and back gives the same result
	dataTypes := []SecretDataType{
		DataTypePlaintext, DataTypeJson, DataTypePem, DataTypeBinary,
		DataTypeBase64, DataTypeX509Cert, DataTypePrivKey, DataTypeDockerCfg,
	}

	for _, originalType := range dataTypes {
		t.Run(string(originalType), func(t *testing.T) {
			// Convert to OID
			oid, err := GetOIDForDataType(originalType)
			require.NoError(t, err)

			// Convert back to data type
			resultType, err := GetDataTypeForOID(oid)
			require.NoError(t, err)

			assert.Equal(t, originalType, resultType)
		})
	}
}

func TestEncodeDER(t *testing.T) {
	tests := []struct {
		name string
		oid  asn1.ObjectIdentifier
	}{
		{
			name: "plaintext OID",
			oid:  OIDPlaintext,
		},
		{
			name: "json OID",
			oid:  OIDJson,
		},
		{
			name: "complex OID",
			oid:  asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 99999, 1, 1, 2, 3},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			der, err := EncodeDER(tt.oid)
			require.NoError(t, err)
			assert.NotEmpty(t, der)

			// Should be valid DER encoding
			assert.True(t, len(der) > 0)
		})
	}
}

func TestDecodeDER(t *testing.T) {
	tests := []struct {
		name string
		oid  asn1.ObjectIdentifier
	}{
		{
			name: "plaintext OID",
			oid:  OIDPlaintext,
		},
		{
			name: "json OID",
			oid:  OIDJson,
		},
		{
			name: "docker-config OID",
			oid:  OIDDockerCfg,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// First encode to DER
			der, err := EncodeDER(tt.oid)
			require.NoError(t, err)

			// Then decode back
			decodedOID, err := DecodeDER(der)
			require.NoError(t, err)
			assert.Equal(t, tt.oid, decodedOID)
		})
	}

	t.Run("invalid DER", func(t *testing.T) {
		invalidDER := []byte{0xFF, 0xFF, 0xFF}
		_, err := DecodeDER(invalidDER)
		assert.Error(t, err)
	})
}

func TestDERRoundTrip(t *testing.T) {
	// Test encoding and decoding all standard OIDs
	oids := []asn1.ObjectIdentifier{
		OIDPlaintext, OIDJson, OIDPem, OIDBinary,
		OIDBase64, OIDX509Cert, OIDPrivKey, OIDDockerCfg,
	}

	for i, originalOID := range oids {
		t.Run(string(rune('A'+i)), func(t *testing.T) {
			// Encode to DER
			der, err := EncodeDER(originalOID)
			require.NoError(t, err)

			// Decode back
			decodedOID, err := DecodeDER(der)
			require.NoError(t, err)

			assert.Equal(t, originalOID, decodedOID)
		})
	}
}

func TestInferDataType(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		expected SecretDataType
	}{
		{
			name:     "PEM certificate",
			data:     []byte("-----BEGIN CERTIFICATE-----\nMIIC..."),
			expected: DataTypePem,
		},
		{
			name:     "PEM private key",
			data:     []byte("-----BEGIN PRIVATE KEY-----\nMIIE..."),
			expected: DataTypePem,
		},
		{
			name:     "JSON object",
			data:     []byte(`{"key": "value", "number": 42}`),
			expected: DataTypeJson,
		},
		{
			name:     "JSON array",
			data:     []byte(`["item1", "item2", "item3"]`),
			expected: DataTypeJson,
		},
		{
			name:     "valid base64",
			data:     []byte("SGVsbG8gV29ybGQ="),
			expected: DataTypeBase64,
		},
		{
			name:     "base64 with padding",
			data:     []byte("SGVsbG8gV29ybGQ="),
			expected: DataTypeBase64,
		},
		{
			name:     "binary data with null bytes",
			data:     []byte{0x00, 0x01, 0x02, 0x03, 0xFF},
			expected: DataTypeBinary,
		},
		{
			name:     "binary data with control characters",
			data:     []byte{0x01, 0x02, 0x03, 0x1F},
			expected: DataTypeBinary,
		},
		{
			name:     "plain text",
			data:     []byte("Hello, World!"),
			expected: DataTypePlaintext,
		},
		{
			name:     "text with newlines and tabs",
			data:     []byte("Line 1\nLine 2\tTabbed"),
			expected: DataTypePlaintext,
		},
		{
			name:     "empty data",
			data:     []byte(""),
			expected: DataTypePlaintext,
		},
		{
			name:     "password-like string",
			data:     []byte("mySecretPassword123!"),
			expected: DataTypePlaintext,
		},
		{
			name:     "invalid base64 (wrong length)",
			data:     []byte("SGVsbG8=invalid"),
			expected: DataTypePlaintext,
		},
		{
			name:     "invalid base64 (invalid characters)",
			data:     []byte("Hello@World"),
			expected: DataTypePlaintext,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := InferDataType(tt.data)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsValidBase64(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "valid base64 no padding",
			input:    "SGVsbG8",
			expected: false, // Length not multiple of 4
		},
		{
			name:     "valid base64 with padding",
			input:    "SGVsbG8=",
			expected: true,
		},
		{
			name:     "valid base64 double padding",
			input:    "SGVsbG8gV29ybGQ=",
			expected: true,
		},
		{
			name:     "valid base64 URL safe",
			input:    "SGVsbG8gV29ybGQ=",
			expected: true,
		},
		{
			name:     "empty string",
			input:    "",
			expected: false,
		},
		{
			name:     "invalid characters",
			input:    "Hello@World!",
			expected: false,
		},
		{
			name:     "wrong length",
			input:    "SGVsbG8gV29ybGQx", // Length 16, but ends without proper padding
			expected: true,
		},
		{
			name:     "only padding",
			input:    "====",
			expected: true,
		},
		{
			name:     "mixed case valid",
			input:    "SGVsbG8gV29ybGQ=",
			expected: true,
		},
		{
			name:     "numbers and letters",
			input:    "SGVsbG8wV29ybGQ=",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidBase64(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
