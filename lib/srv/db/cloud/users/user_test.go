/*
Copyright 2022 Gravitational, Inc.

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

package users

import (
	"context"
	"fmt"
	"testing"
	"time"

	libsecrets "github.com/gravitational/teleport/lib/secrets"

	"github.com/gravitational/trace"
	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"
)

func TestBaseUser(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	clock := clockwork.NewFakeClock()
	modifyUserPassword := make(chan string, 1)

	secrets, err := libsecrets.NewAWSSecretsManager(libsecrets.AWSSecretsManagerConfig{
		Client: libsecrets.NewMockSecretsManagerClient(libsecrets.MockSecretsManagerClientConfig{
			Clock: clock,
		}),
	})
	require.NoError(t, err)

	user := &baseUser{
		secrets:                     secrets,
		secretKey:                   "local/testuser",
		secretTTL:                   time.Hour,
		inDatabaseName:              "testuser",
		maxPasswordLength:           10,
		usePreviousPasswordForLogin: true,
		clock:                       clock,
		modifyUserFunc: func(ctx context.Context, oldPassword, newPassword string) error {
			modifyUserPassword <- newPassword
			return nil
		},
	}

	t.Run("CheckAndSetDafaults", func(t *testing.T) {
		require.NoError(t, user.CheckAndSetDefaults())
		require.Equal(t, "local/testuser", user.GetID())
		require.Equal(t, "local/testuser", fmt.Sprintf("%v", user))
		require.Equal(t, "testuser", user.GetInDatabaseName())
	})

	t.Run("Setup", func(t *testing.T) {
		require.NoError(t, user.Setup(ctx))
		require.Len(t, modifyUserPassword, 1)
		passwordSet := <-modifyUserPassword

		// Validate password set for the cloud user is the same one fetched from secrets store.
		password, err := user.GetPassword(ctx)
		require.NoError(t, err)
		require.Equal(t, password, passwordSet)

		// Setup a second time should not fail, and nothing happens.
		require.NoError(t, user.Setup(ctx))
		require.Len(t, modifyUserPassword, 0)
	})

	t.Run("RotatePassword not expired", func(t *testing.T) {
		require.NoError(t, user.RotatePassword(ctx))
		require.Len(t, modifyUserPassword, 0)

		clock.Advance(user.secretTTL / 2)
		require.NoError(t, user.RotatePassword(ctx))
		require.Len(t, modifyUserPassword, 0)
	})

	t.Run("RotatePassword expired", func(t *testing.T) {
		clock.Advance(user.secretTTL * 2)

		require.NoError(t, user.RotatePassword(ctx))
		require.Len(t, modifyUserPassword, 1)
		passwordSet := <-modifyUserPassword

		// Validate password set for the cloud user is the same one saved in secrets store.
		currentVersion, err := secrets.GetValue(ctx, "local/testuser", libsecrets.CurrentVersion)
		require.NoError(t, err)
		require.Equal(t, currentVersion.Value, passwordSet)

		// Successfully rotated once, now should use previous version for login.
		previousVersion, err := secrets.GetValue(ctx, "local/testuser", libsecrets.PreviousVersion)
		require.NoError(t, err)

		password, err := user.GetPassword(ctx)
		require.NoError(t, err)
		require.Equal(t, previousVersion.Value, password)
	})

	t.Run("RotatePassword secret not found", func(t *testing.T) {
		// Simulate a case that someone else has deleted the secret.
		require.NoError(t, secrets.Delete(ctx, "local/testuser"))

		require.NoError(t, user.RotatePassword(ctx))
		require.Len(t, modifyUserPassword, 1)
		passwordSet := <-modifyUserPassword

		password, err := user.GetPassword(ctx)
		require.NoError(t, err)
		require.Equal(t, password, passwordSet)
	})

	t.Run("Teardown", func(t *testing.T) {
		require.NoError(t, user.Teardown(ctx))

		_, err := secrets.GetValue(ctx, "local/testuser", libsecrets.CurrentVersion)
		require.True(t, trace.IsNotFound(err))
	})
}