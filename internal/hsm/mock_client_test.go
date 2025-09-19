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
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewMockClient(t *testing.T) {
	client := NewMockClient()

	assert.NotNil(t, client)
	assert.False(t, client.IsConnected())
	assert.NotNil(t, client.secrets)
	assert.NotNil(t, client.metadata)
}

func TestMockClientInitialize(t *testing.T) {
	client := NewMockClient()
	ctx := context.Background()

	config := Config{
		PKCS11LibraryPath: "/test/lib.so",
		PINProvider:       NewStaticPINProvider("testpin"),
		SlotID:            1,
	}

	err := client.Initialize(ctx, config)
	require.NoError(t, err)

	assert.True(t, client.IsConnected())
	assert.Equal(t, config, client.config)

	// Check that pre-populated data exists
	secrets := client.GetAllSecrets()
	assert.Contains(t, secrets, "secrets/default/test-secret")
	assert.Contains(t, secrets, "secrets/production/database-credentials")
}

func TestMockClientClose(t *testing.T) {
	client := NewMockClient()
	ctx := context.Background()

	// Initialize first
	err := client.Initialize(ctx, DefaultConfig())
	require.NoError(t, err)
	assert.True(t, client.IsConnected())

	// Close connection
	err = client.Close()
	require.NoError(t, err)
	assert.False(t, client.IsConnected())
}

func TestMockClientGetInfo(t *testing.T) {
	client := NewMockClient()
	ctx := context.Background()

	t.Run("not connected", func(t *testing.T) {
		info, err := client.GetInfo(ctx)
		assert.Error(t, err)
		assert.Nil(t, info)
		assert.Contains(t, err.Error(), "HSM not connected")
	})

	t.Run("connected", func(t *testing.T) {
		err := client.Initialize(ctx, DefaultConfig())
		require.NoError(t, err)

		info, err := client.GetInfo(ctx)
		require.NoError(t, err)
		assert.Equal(t, "Mock HSM Token", info.Label)
		assert.Equal(t, "Test Manufacturer", info.Manufacturer)
		assert.Equal(t, "Mock HSM v1.0", info.Model)
		assert.Equal(t, "MOCK123456", info.SerialNumber)
		assert.Equal(t, "1.0.0", info.FirmwareVersion)
	})
}

func TestMockClientReadSecret(t *testing.T) {
	client := NewMockClient()
	ctx := context.Background()

	t.Run("not connected", func(t *testing.T) {
		data, err := client.ReadSecret(ctx, "test/path")
		assert.Error(t, err)
		assert.Nil(t, data)
		assert.Contains(t, err.Error(), "HSM not connected")
	})

	t.Run("secret not found", func(t *testing.T) {
		err := client.Initialize(ctx, DefaultConfig())
		require.NoError(t, err)

		data, err := client.ReadSecret(ctx, "nonexistent/path")
		assert.Error(t, err)
		assert.Nil(t, data)
		assert.Contains(t, err.Error(), "secret not found")
	})

	t.Run("read existing secret", func(t *testing.T) {
		err := client.Initialize(ctx, DefaultConfig())
		require.NoError(t, err)

		data, err := client.ReadSecret(ctx, "secrets/default/test-secret")
		require.NoError(t, err)
		assert.Equal(t, []byte("testuser"), data["username"])
		assert.Equal(t, []byte("testpass123"), data["password"])
	})
}

func TestMockClientWriteSecret(t *testing.T) {
	client := NewMockClient()
	ctx := context.Background()

	err := client.Initialize(ctx, DefaultConfig())
	require.NoError(t, err)

	data := SecretData{"data": []byte("test-data")}
	metadata := &SecretMetadata{
		Description: "Test secret with metadata",
		Labels:      map[string]string{"env": "test"},
		Format:      "raw",
		DataType:    "plaintext",
		CreatedAt:   "2025-01-01T00:00:00Z",
		Source:      "test",
	}

	err = client.WriteSecret(ctx, "test/with-metadata", data, metadata)
	require.NoError(t, err)

	// Verify data was written
	readData, err := client.ReadSecret(ctx, "test/with-metadata")
	require.NoError(t, err)
	assert.Equal(t, data, readData)

	// Verify metadata was written
	readMetadata, err := client.ReadMetadata(ctx, "test/with-metadata")
	require.NoError(t, err)
	assert.Equal(t, metadata, readMetadata)
}

func TestMockClientReadMetadata(t *testing.T) {
	client := NewMockClient()
	ctx := context.Background()

	t.Run("not connected", func(t *testing.T) {
		metadata, err := client.ReadMetadata(ctx, "test/path")
		assert.Error(t, err)
		assert.Nil(t, metadata)
		assert.Contains(t, err.Error(), "HSM not connected")
	})

	t.Run("metadata not found", func(t *testing.T) {
		err := client.Initialize(ctx, DefaultConfig())
		require.NoError(t, err)

		metadata, err := client.ReadMetadata(ctx, "nonexistent/path")
		assert.Error(t, err)
		assert.Nil(t, metadata)
		assert.Contains(t, err.Error(), "metadata not found")
	})

	t.Run("read existing metadata", func(t *testing.T) {
		err := client.Initialize(ctx, DefaultConfig())
		require.NoError(t, err)

		// Write secret with metadata first
		data := SecretData{"data": []byte("test")}
		metadata := &SecretMetadata{
			Description: "Test metadata",
			Labels:      map[string]string{"type": "test"},
		}
		err = client.WriteSecret(ctx, "test/metadata", data, metadata)
		require.NoError(t, err)

		// Read back metadata
		readMetadata, err := client.ReadMetadata(ctx, "test/metadata")
		require.NoError(t, err)
		assert.Equal(t, metadata, readMetadata)
	})
}

func TestMockClientDeleteSecret(t *testing.T) {
	client := NewMockClient()
	ctx := context.Background()

	t.Run("not connected", func(t *testing.T) {
		err := client.DeleteSecret(ctx, "test/path")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "HSM not connected")
	})

	t.Run("secret not found", func(t *testing.T) {
		err := client.Initialize(ctx, DefaultConfig())
		require.NoError(t, err)

		err = client.DeleteSecret(ctx, "nonexistent/path")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "secret not found")
	})

	t.Run("delete existing secret", func(t *testing.T) {
		err := client.Initialize(ctx, DefaultConfig())
		require.NoError(t, err)

		data := SecretData{"key": []byte("value")}
		err = client.WriteSecret(ctx, "test/delete", data, nil)
		require.NoError(t, err)

		// Verify it exists
		_, err = client.ReadSecret(ctx, "test/delete")
		require.NoError(t, err)

		// Delete it
		err = client.DeleteSecret(ctx, "test/delete")
		require.NoError(t, err)

		// Verify it's gone
		_, err = client.ReadSecret(ctx, "test/delete")
		assert.Error(t, err)
	})

	t.Run("delete secret with metadata", func(t *testing.T) {
		err := client.Initialize(ctx, DefaultConfig())
		require.NoError(t, err)

		data := SecretData{"key": []byte("value")}
		metadata := &SecretMetadata{Description: "To be deleted"}
		err = client.WriteSecret(ctx, "test/delete-with-meta", data, metadata)
		require.NoError(t, err)

		// Delete the secret
		err = client.DeleteSecret(ctx, "test/delete-with-meta")
		require.NoError(t, err)

		// Verify both secret and metadata are gone
		_, err = client.ReadSecret(ctx, "test/delete-with-meta")
		assert.Error(t, err)

		_, err = client.ReadMetadata(ctx, "test/delete-with-meta")
		assert.Error(t, err)
	})
}

func TestMockClientListSecrets(t *testing.T) {
	client := NewMockClient()
	ctx := context.Background()

	t.Run("not connected", func(t *testing.T) {
		paths, err := client.ListSecrets(ctx, "")
		assert.Error(t, err)
		assert.Nil(t, paths)
		assert.Contains(t, err.Error(), "HSM not connected")
	})

	t.Run("list all secrets", func(t *testing.T) {
		err := client.Initialize(ctx, DefaultConfig())
		require.NoError(t, err)

		paths, err := client.ListSecrets(ctx, "")
		require.NoError(t, err)
		assert.Contains(t, paths, "secrets/default/test-secret")
		assert.Contains(t, paths, "secrets/production/database-credentials")
	})

	t.Run("list with prefix filter", func(t *testing.T) {
		err := client.Initialize(ctx, DefaultConfig())
		require.NoError(t, err)

		// Add some test secrets
		err = client.WriteSecret(ctx, "app/config", SecretData{"key": []byte("value")}, nil)
		require.NoError(t, err)
		err = client.WriteSecret(ctx, "app/database", SecretData{"key": []byte("value")}, nil)
		require.NoError(t, err)
		err = client.WriteSecret(ctx, "other/secret", SecretData{"key": []byte("value")}, nil)
		require.NoError(t, err)

		// List with "app/" prefix
		paths, err := client.ListSecrets(ctx, "app/")
		require.NoError(t, err)
		assert.Contains(t, paths, "app/config")
		assert.Contains(t, paths, "app/database")
		assert.NotContains(t, paths, "other/secret")

		// List with "secrets/" prefix
		paths, err = client.ListSecrets(ctx, "secrets/")
		require.NoError(t, err)
		assert.Contains(t, paths, "secrets/default/test-secret")
		assert.Contains(t, paths, "secrets/production/database-credentials")
		assert.NotContains(t, paths, "app/config")
	})
}

func TestMockClientGetChecksum(t *testing.T) {
	client := NewMockClient()
	ctx := context.Background()

	t.Run("checksum of nonexistent secret", func(t *testing.T) {
		err := client.Initialize(ctx, DefaultConfig())
		require.NoError(t, err)

		checksum, err := client.GetChecksum(ctx, "nonexistent/path")
		assert.Error(t, err)
		assert.Empty(t, checksum)
	})

	t.Run("checksum of existing secret", func(t *testing.T) {
		err := client.Initialize(ctx, DefaultConfig())
		require.NoError(t, err)

		data := SecretData{
			"username": []byte("testuser"),
			"password": []byte("testpass"),
		}
		err = client.WriteSecret(ctx, "test/checksum", data, nil)
		require.NoError(t, err)

		checksum, err := client.GetChecksum(ctx, "test/checksum")
		require.NoError(t, err)
		assert.Contains(t, checksum, "sha256:")
		assert.True(t, len(checksum) > 10)

		// Verify checksum is consistent
		checksum2, err := client.GetChecksum(ctx, "test/checksum")
		require.NoError(t, err)
		assert.Equal(t, checksum, checksum2)
	})
}

func TestMockClientIsConnected(t *testing.T) {
	client := NewMockClient()

	// Initially not connected
	assert.False(t, client.IsConnected())

	// After initialization
	ctx := context.Background()
	err := client.Initialize(ctx, DefaultConfig())
	require.NoError(t, err)
	assert.True(t, client.IsConnected())

	// After close
	err = client.Close()
	require.NoError(t, err)
	assert.False(t, client.IsConnected())
}

func TestMockClientAddSecret(t *testing.T) {
	client := NewMockClient()

	data := SecretData{
		"username": []byte("testuser"),
		"password": []byte("testpass"),
	}

	client.AddSecret("test/added", data)

	// Should be able to read it after initialization
	ctx := context.Background()
	err := client.Initialize(ctx, DefaultConfig())
	require.NoError(t, err)

	readData, err := client.ReadSecret(ctx, "test/added")
	require.NoError(t, err)
	assert.Equal(t, data, readData)
}

func TestMockClientGetAllSecrets(t *testing.T) {
	client := NewMockClient()
	ctx := context.Background()

	err := client.Initialize(ctx, DefaultConfig())
	require.NoError(t, err)

	// Add a test secret
	data := SecretData{"key": []byte("value")}
	err = client.WriteSecret(ctx, "test/all-secrets", data, nil)
	require.NoError(t, err)

	allSecrets := client.GetAllSecrets()

	// Should contain pre-populated secrets
	assert.Contains(t, allSecrets, "secrets/default/test-secret")
	assert.Contains(t, allSecrets, "secrets/production/database-credentials")

	// Should contain our test secret
	assert.Contains(t, allSecrets, "test/all-secrets")
	assert.Equal(t, data, allSecrets["test/all-secrets"])
}

func TestMockClientDataRaceProtection(t *testing.T) {
	client := NewMockClient()
	ctx := context.Background()

	err := client.Initialize(ctx, DefaultConfig())
	require.NoError(t, err)

	// Simulate concurrent access
	done := make(chan bool, 2)

	// Writer goroutine
	go func() {
		for i := 0; i < 100; i++ {
			data := SecretData{"key": []byte("value")}
			_ = client.WriteSecret(ctx, "concurrent/test", data, nil)
		}
		done <- true
	}()

	// Reader goroutine
	go func() {
		for i := 0; i < 100; i++ {
			_, _ = client.ReadSecret(ctx, "concurrent/test")
			_, _ = client.ListSecrets(ctx, "concurrent/")
		}
		done <- true
	}()

	// Wait for both goroutines
	<-done
	<-done

	// Should not panic due to data races
}
