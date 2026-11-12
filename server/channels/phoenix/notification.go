// Copyright (c) Phoenix-Soft-VN. Licensed under the surrounding fork terms.
package phoenix

import (
	"context"
	"github.com/lib/pq"
	"time"
)

// NotificationAllowed rechecks one background delivery, including queued email
// batches, independently of the sender's request/session. Channel ownership and
// native membership are checked together in one indexed query for this recipient.
// No customer binding ever becomes an operator or an offline authority fallback.
func (p *Policy) NotificationAllowed(ctx context.Context, userID string, channelIDs []string) bool {
	if userID == "" || len(channelIDs) == 0 || len(channelIDs) > MaxBatchIDs {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	var actor ActorContext
	var kind string
	err := p.db.QueryRowContext(ctx, `SELECT sb.session_id,sb.issuer,sb.sub,sb.sid,sb.tenant_id,ub.native_user_id,ub.actor_kind,ub.access_version,tb.team_id
 FROM phoenix_user_bindings ub
 JOIN phoenix_tenant_bindings tb ON tb.tenant_id=ub.tenant_id AND tb.state='ACTIVE'
 JOIN teams t ON t.id=tb.team_id AND t.type='I' AND NOT t.allowopeninvite AND t.deleteat=0
 JOIN phoenix_session_bindings sb ON sb.native_user_id=ub.native_user_id AND sb.issuer=ub.issuer AND sb.sub=ub.sub AND sb.tenant_id=ub.tenant_id
 JOIN sessions s ON s.id=sb.session_id AND s.userid=ub.native_user_id
 WHERE ub.native_user_id=$1 AND ub.access_state='GRANTED' AND sb.expires_at>$3 AND s.expiresat>$3
 AND NOT EXISTS(SELECT 1 FROM teammembers tm WHERE tm.userid=$1 AND tm.teamid<>tb.team_id AND tm.deleteat=0)
 AND NOT EXISTS(SELECT 1 FROM unnest($2::text[]) requested(id)
 LEFT JOIN channels c ON c.id=requested.id AND c.deleteat=0
 LEFT JOIN channelmembers cm ON cm.channelid=c.id AND cm.userid=$1
 LEFT JOIN phoenix_channel_bindings cb ON cb.channel_id=c.id
 WHERE c.id IS NULL OR cm.userid IS NULL OR NOT COALESCE(c.teamid=tb.team_id OR (c.teamid='' AND cb.tenant_id=ub.tenant_id),false))
 ORDER BY sb.expires_at DESC,sb.session_id LIMIT 1`, userID, pq.Array(channelIDs), p.now().UnixMilli()).Scan(
		&actor.SessionID, &actor.Issuer, &actor.Sub, &actor.SID, &actor.TenantID, &actor.UserID, &kind, &actor.AccessVersion, &actor.TeamID)
	if err != nil || p.driftError(actor.TenantID) != nil {
		return false
	}
	actor.Kind = ActorKind(kind)
	return p.Authority == nil || p.Authority.Check(ctx, actor) == nil
}
