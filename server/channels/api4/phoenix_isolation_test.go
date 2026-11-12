// Copyright (c) Phoenix-Soft-VN. See LICENSE.txt.
package api4

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/v8/channels/app"
	"github.com/mattermost/mattermost/server/v8/channels/phoenix"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// SESSION-COMPENSATION: exercise the actual native adapter/session store, not a session double.
// Break: omit native revoke when the Phoenix binding write fails, or release cookies early.
func TestPhoenixSessionCompensation(t *testing.T) {
	th := Setup(t).InitBasic(t)
	adapter := app.NewPhoenixNative(th.App)
	rec := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "https://chat.mm-replace.test/phoenix/auth/callback", nil)
	var created string
	_, err := adapter.NewSession(context.Background(), rec, request, th.BasicUser.Id, func(id string, _ int64) error { created = id; return errors.New("injected Phoenix binding failure") })
	require.Error(t, err)
	require.NotEmpty(t, created)
	var count int
	err = th.App.Srv().Store().GetInternalMasterDB().QueryRow(`SELECT count(*) FROM sessions WHERE id=$1`, created).Scan(&count)
	require.NoError(t, err)
	require.Zero(t, count, "orphan session must be revoked")
	for _, cookie := range rec.Result().Cookies() {
		require.NotEqual(t, "MMAUTHTOKEN", cookie.Name, "failed binding must release no native session cookie")
	}
}

// Break: guarding only a policy helper while native HTTP/native-admin/bulk/DM still crosses tenants.
func TestPhoenixIsolationHTTP(t *testing.T) {
	th := Setup(t).InitBasic(t)
	ctx := context.Background()
	foreign := th.CreateUser(t)
	teamB := th.CreateTeamWithClient(t, th.SystemAdminClient)
	_, err := th.SystemAdminClient.UpdateUserRoles(ctx, th.BasicUser.Id, "system_user system_admin")
	require.NoError(t, err)
	db := th.App.Srv().Store().GetInternalMasterDB()
	_, err = db.Exec(`UPDATE teams SET type='I',allowopeninvite=false WHERE id IN ($1,$2)`, th.BasicTeam.Id, teamB.Id)
	require.NoError(t, err)
	tenantA := "tenant-a-" + model.NewId()
	tenantB := "tenant-b-" + model.NewId()
	now := time.Now().UnixMilli()
	_, err = db.Exec(`INSERT INTO phoenix_tenant_bindings(tenant_id,team_id,state,policy_version,created_at,updated_at) VALUES($1,$2,'ACTIVE',1,$5,$5),($3,$4,'ACTIVE',1,$5,$5)`, tenantA, th.BasicTeam.Id, tenantB, teamB.Id, now)
	require.NoError(t, err)
	for _, row := range []struct{ user, sub, tenant string }{{th.BasicUser.Id, "a1", tenantA}, {th.BasicUser2.Id, "a2", tenantA}, {foreign.Id, "b1", tenantB}} {
		_, err = db.Exec(`INSERT INTO phoenix_user_bindings(issuer,sub,tenant_id,native_user_id,actor_kind,access_state,access_version,created_at,updated_at) VALUES('https://auth.mm-replace.test',$1,$2,$3,'CUSTOMER','GRANTED',1,$4,$4)`, row.sub, row.tenant, row.user, now)
		require.NoError(t, err)
	}
	session, appErr := th.App.GetSession(th.Client.AuthToken)
	require.Nil(t, appErr)
	_, err = db.Exec(`INSERT INTO phoenix_session_bindings(session_id,issuer,sub,sid,tenant_id,native_user_id,access_version,expires_at) VALUES($1,'https://auth.mm-replace.test','a1','sid-a1',$2,$3,1,$4)`, session.Id, tenantA, th.BasicUser.Id, time.Now().Add(time.Hour).UnixMilli())
	require.NoError(t, err)
	policy := phoenix.NewPolicy(db, nil)
	policy.SetDrift("", nil)
	old := phoenix.Default()
	phoenix.SetDefault(policy)
	defer phoenix.SetDefault(old)
	_, response, err := th.Client.GetUser(ctx, th.BasicUser2.Id, "")
	require.NoError(t, err)
	require.Equal(t, 200, response.StatusCode)
	_, response, err = th.Client.GetUser(ctx, foreign.Id, "")
	require.Error(t, err)
	require.Equal(t, 404, response.StatusCode)
	// A tenant bot must provision without an unbound cross-tenant creator DM.
	store := phoenix.NewStore(db, nil)
	require.NoError(t, store.RegisterOperator(ctx, th.SystemAdminUser.Id, "native-fixture"))
	adapter := app.NewPhoenixNative(th.App)
	service := phoenix.NewService(store, adapter)
	request := phoenix.NotifyRequest{Caller: "native-fixture", OperationKey: model.NewId(), TenantID: tenantA,
		ChannelID: th.BasicChannel.Id, Message: "tenant bot positive control"}
	first, notifyErr := service.Notify(ctx, adapter, request)
	require.NoError(t, notifyErr)
	again, notifyErr := service.Notify(ctx, adapter, request)
	require.NoError(t, notifyErr)
	require.Equal(t, first.PostID, again.PostID)
	allowed, _, err := th.Client.GetUsersByIds(ctx, []string{th.BasicUser.Id, th.BasicUser2.Id, foreign.Id, "zzzzzzzzzzzzzzzzzzzzzzzzzz"})
	require.NoError(t, err)
	require.Len(t, allowed, 2)
	// Regression: the HTTP cap applies before the native parser deduplicates.
	batch := make([]string, 201)
	for i := range batch {
		batch[i] = th.BasicUser.Id
	}
	blob, err := json.Marshal(batch)
	require.NoError(t, err)
	for _, route := range []string{"/users/ids", "/users/status/ids"} {
		request, err := http.NewRequest("POST", th.Client.URL+"/api/v4"+route, bytes.NewReader(blob))
		require.NoError(t, err)
		request.Header.Set("Authorization", "Bearer "+th.Client.AuthToken)
		request.Header.Set("Content-Type", "application/json")
		result, err := th.Client.HTTPClient.Do(request)
		require.NoError(t, err)
		result.Body.Close()
		require.Equal(t, 400, result.StatusCode, route+" repeated IDs must still count")
	}
	_, response, err = th.Client.GetUserByEmail(ctx, th.BasicUser2.Email, "")
	require.NoError(t, err)
	require.Equal(t, 200, response.StatusCode)
	_, response, err = th.Client.GetUserByEmail(ctx, foreign.Email, "")
	require.Error(t, err)
	require.Equal(t, 404, response.StatusCode)
	_, response, err = th.Client.CreateDirectChannel(ctx, th.BasicUser.Id, foreign.Id)
	require.Error(t, err)
	require.Equal(t, 404, response.StatusCode)
}
