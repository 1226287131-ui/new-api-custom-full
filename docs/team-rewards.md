# My Team: Invitation Rewards and Withdrawals

## Scope

The new pages are `/team` (signed-in users) and `/team/manage` (administrators).
They reuse the wallet's existing `/sign-up?aff=...` invitation link and the
existing `inviter_id` relationship. Only direct invitations earn rewards. No
multi-level rewards or automatic transfer to a payment provider is implemented.
This feature is disabled by default and has no licensing requirement.

The first version supports verified **CNY Epay recharge orders only**. Stripe,
Creem, Waffo, other payment providers and subscription purchases do not create
these rewards. Their amount/currency contracts must be integrated explicitly
before enabling rewards on them.

Manual balance changes, manual completion of recharge orders, redemption codes,
gifts, and historical orders without a reward snapshot do not earn rewards.
Existing registration-time invitation rewards are unchanged.

## Amounts and Policy

Only the root administrator can change the global reward policy. It includes
the enable switch, default reward type, separate credit/cash percentages, holding period,
and minimum cash withdrawal. Percentages have two-decimal precision.

Users can choose credit or cash rewards on `/team`; they cannot set the rates.
Users without a saved choice follow the administrator's default. The preference
belongs to the inviter earning the reward, not to the person recharging.
Cash selection requires the payout encryption key to be configured.

Each online order snapshots its inviter, policy and conversion when created:

```
credit reward = actual paid CNY * credit percentage / order-time Price
internal quota = credit reward * order-time QuotaPerUnit
cash reward CNY = actual paid CNY * cash percentage
```

For a CNY 100 payment and a 5% reward, `Price=0.1` earns 50 site credits,
`Price=1` earns 5 site credits, and cash mode earns CNY 5. The formula uses the
actual paid amount after recharge discounts, not the undiscounted order amount.
Internal quota truncates fractional quota units; cash rounds to the nearest
cent. Policy changes affect new orders only. Already-snapshotted orders retain
their terms, including their holding period, even when the policy is disabled.
Changing a personal reward type likewise affects newly created orders only.

Administrators can search users and change or remove their direct inviter on
`/team/manage`. The server requires email verification or an enrolled strong factor, verifies the
administrator's current role, rejects self-invitations and cycles, and binds a
single-use proof to the user, previous inviter, new inviter and change reason.
Concurrent edits use an expected-inviter check and a database guard. The change
and its audit record commit together. Existing recharge snapshots, rewards,
balances and registration-time invitation counters are not recalculated.

Credit rewards go into spendable account quota. Cash rewards use a separate
wallet and cannot be spent as API quota. Site credits cannot be withdrawn.

## Settlement and Safety

- Epay callbacks verify the signature, merchant ID, order provider and exact
  paid amount. The success acknowledgement follows the database transaction.
- Order success, recharge quota and a unique reward event commit together.
  Duplicate callbacks do not create duplicate recharge credits or rewards.
- A master-node worker processes due events every 15 seconds (up to 100 per
  pass). Reward credit and the settled marker commit in one transaction.
  Failed grants remain pending and retry after 60 seconds.
- Database balance credits carry a cumulative watermark for idempotent Redis
  synchronization. Synchronization must preserve pending request deductions;
  it must not delete the balance cache or blindly replay a credit increment.
  If the process stops or Redis fails immediately after the database commit,
  the credit remains in the database but may not be visible in the cache yet.
  A repeated payment callback reconciles the payer's recharge watermark, not
  the inviter's reward. A fresh database snapshot or cache rebuild reconciles
  the reward watermark. Cache TTL uses `SyncFrequency` with a 60-second fallback;
  this is not a hard recovery-time bound, particularly if other operations
  renew the cache. There is no separate durable cache-retry queue.
- Amount and balance bounds prevent integer overflow. A reward that would
  exceed the recipient balance limit remains pending for administrator review;
  it is not silently dropped. New Epay orders check payer balance capacity
  before presenting the payment URL, and settlement checks it again.
- Balances and reward events are in the primary database. The separate human-
  readable recharge log is not the transaction's source of truth.

These guarantees concern this feature's transactions, not a promise of zero
data loss from database/storage outages or unrelated request-billing paths.

## Alipay and Manual Withdrawals

Configure `AGENT_PAYOUT_ENCRYPTION_KEY` with a base64-encoded, cryptographically
random 32-byte key before enabling cash rewards or binding payout accounts.
Use a secret store or a restricted environment file, never a committed file.
Back up this key securely alongside database recovery materials. Losing or
replacing it without a migration makes saved payout accounts unreadable.
All instances sharing the database must use the same key.

Normal APIs return masked Alipay account/name values only. Full details require
an administrator's security proof; the read is audited without recording the
plaintext account. Binding or changing Alipay details defaults to email
verification; enrolled 2FA and passkeys remain available. Withdrawal requests,
payout disclosure and review still require 2FA or a passkey bound to the
current session. For those operations, open `/security` to enroll a factor if
needed. A locked factor or unsupported passkey device retains its specific
error rather than being treated as missing enrollment.

### Email Verification for Team Changes

Only `team.payout.write` and `team.referral.write` allow email verification.
The operator must already have a bound email address and the site's existing
SMTP settings must work. Referral changes send the code to the administrator's
own bound email, not the member or inviter. Clients cannot choose the recipient.
Missing email or unavailable SMTP never bypasses verification.

`POST /api/verify/email/send` accepts `scope`, `context` and an optional
`flow_token` for resending. It returns the masked email, flow token, expiry and
resend time. `POST /api/verify` accepts `method: "email"`, the same scope/context,
flow token and six-digit code. Payout context contains `account` and `name`;
referral context contains `user_id`, `expected_inviter_id`, `inviter_id` and
`reason`. The resulting security proof is single-use and bound to that action.

- Codes use the existing cryptographically random generator, are stored as
  salted password hashes, and expire within an account-wide 10-minute window.
- Resending is limited to once per 60 seconds. Five incorrect submissions lock
  email verification until the window expires. Resending, changing sessions or
  starting a new flow does not reset the error budget or extend the window.
- A replacement code activates only after SMTP accepts the email. Delivery
  failure preserves the previous valid code; SMTP acceptance cannot guarantee
  inbox delivery.
- Verification binds the user, session, email snapshot, scope and exact action
  details. The final business transaction rechecks the session and email;
  referral writes also recheck the administrator's role.
- These APIs require explicit dashboard `Authorization` and a live session.
  Cookies alone and personal access tokens cannot authorize them. Audit events
  exclude codes, usable tokens and plaintext payout details.

This extension reuses `AuthFlow` and adds no tables or schema migrations. The
UI reuses the existing verification dialog and countdown hook, with all seven
locales updated. Matching frontend/backend versions are required because payout
proofs now include the specific account and name.

Email confirmation is a convenience/security tradeoff requested for these two
actions, not strong MFA. Compromise of both the login session and mailbox can
authorize them. Login, email binding, MFA settings and withdrawal authorization
are not relaxed. Protect the mailbox and retain stronger verification for
financial review and payout operations.

The withdrawal workflow is:

1. The user binds their Alipay account and submits an amount.
2. The database atomically moves cash from available to reserved and creates
   the withdrawal. A per-user request ID makes retries idempotent.
3. An administrator approves it, checks the secured payout details and transfers
   the money manually outside this application.
4. The administrator marks it paid with the external payment reference. This
   consumes reserved funds exactly once. The application does not send money.
5. Alternatively, rejecting a pending/approved request requires a reason and
   releases reserved funds exactly once.

Switching to credit mode does not prevent withdrawal of previously earned cash
while the feature remains enabled. Disabling the feature blocks new withdrawal
requests but keeps existing records and administrator review available.
An in-flight withdrawal retains its payout-account snapshot if the user later
changes the bound account.

## Rollout and Known Limits

This change adds eight feature tables, order snapshot columns and the user credit
watermark. Back up the database and review migrations on a staging copy before
production rollout. MySQL/PostgreSQL use row locks; SQLite uses the shared
locking helper's dialect-safe behavior. Execute dialect-specific acceptance
tests against the actual deployed database as well as local SQLite tests.

Keep the feature disabled until payment settings, encryption key, verification
methods and a small end-to-end payment/withdrawal have been checked. Deploy
matching code and secrets to all instances that handle the shared database.

Do not perform a mixed-version rolling upgrade across instances sharing Redis
and the database. Old code can rebuild quota hashes without the credit
watermark, so it cannot safely share live balance caches with this version.
Drain requests, finish pending balance writes, stop all old application
instances, and initialize this version with clean user balance hashes before
resuming traffic. This applies even while invitation rewards are disabled,
because verified Epay recharge settlement also uses the credit watermark.
Never clear live balance hashes while request deductions are still pending.

**Refund reversal is not automated in this version.** A payment refund or charge-
back does not automatically cancel a pending reward or reclaim a settled one.
Administrators must reconcile refunds before approving payouts; a holding
period reduces exposure but is not a substitute for refund integration.

Changing labels does not establish legal compliance. Operation of invitation
rewards and cash payouts must be reviewed under the applicable rules.

## Main-Branch Compatibility and Verification

The integration uses the current main-branch single-use security proof API.
Team operations retain their separate scopes and do not automatically replay
requests after consuming a proof. Payout binding and referral changes allow
email verification; other sensitive team operations require 2FA or passkeys. Quota credits use
the existing wallet-wide limit rather than the smaller per-request billing
limit. Existing task, video, pricing, gateway and other payment-provider paths
remain on the main-branch implementation.

Focused regression coverage includes payment callback validation and
idempotency, reward settlement, manual-credit exclusions, withdrawals,
quota/auth-cache consistency, security proof consumption and frontend
verification cancellation. The frontend tests run under the project's Vitest
runner. Also run the full frontend typecheck, production build, root Go build,
and the independent `GOWORK=off go build ./...` inside `relaykit`.

For release acceptance, restore a backup into an isolated database and invoke
only the database initialization/migration code, without application background
workers. Compare balances, orders, usage totals and task totals before and after
migration, and repeat migration to verify restart safety. Do not test payment
callbacks or create artificial reward transactions against production users.
Real-money payment and withdrawal acceptance remains an explicit operator step
before enabling rewards.

The sensitive-action design follows the OWASP Authentication, Session
Management and Transaction Authorization Cheat Sheets: server-side role
enforcement, scoped secondary verification, session/action-bound proofs,
single use, and secret-free audit records. Focused tests exercise changed-action
proof rejection, replay rejection, stale edits and unchanged historical orders.
These checks are not a certification of full ASVS compliance.

The email extension was checked against OWASP ASVS 5.0.0 controls for single
use and hashed random codes (6.5.1-6.5.5), request binding and throttling
(6.6.2-6.6.3), server-side session verification and revocation (7.2.1, 7.4.1),
and server-side authorization (8.3.1). Email does not meet the strong out-of-band
authentication guidance in V6.6 or L3 requirement 6.3.6. This documented
exception is limited to the two actions above and must not be presented as
ASVS L2/L3 certification.

The 2026-09-20 preference/referral extension was verified with real SQLite
3.50.4, MySQL 8.4.6 and PostgreSQL 17.6. Each engine passed fresh-install and
upgrade scenarios with repeated migrations, unchanged historical financial
records, preference upserts, concurrent cycle prevention and audit rollback:

```sh
go test ./model -run '^TestAgentRewardReferralDatabaseCompatibility$' -count=1 -v
go test ./model ./controller ./router ./service -run 'Test(Agent|Team)' -count=1
```

External-engine scenarios require empty scratch databases configured through
`NEWAPI_TEAM_TEST_MYSQL_FRESH_DSN`, `NEWAPI_TEAM_TEST_MYSQL_UPGRADE_DSN`,
`NEWAPI_TEAM_TEST_POSTGRES_FRESH_DSN` and `NEWAPI_TEAM_TEST_POSTGRES_UPGRADE_DSN`.
They skip when the corresponding DSN is absent and refuse nonempty databases.
The minimum supported MySQL/PostgreSQL releases were not separately exercised;
no version-specific SQL was introduced. This matrix does not replace live
payment acceptance or multi-instance financial settlement stress testing.

The email extension also passed real SQLite 3.50.4, MySQL 8.4.6 and PostgreSQL
17.6 tests on 2026-09-20, including successful payout/referral writes, changed
email and role rejection, expiry, replay, shared attempt budgets, concurrent
sends/consumption, and preservation of the old code on SMTP failure:

```sh
go test ./service -run '^TestTeamEmailVerificationDatabaseMatrix$' -count=1 -v
go test ./service ./model ./controller ./router ./middleware -run 'Test(Team|AuthFlow|ConsumeAuthFlow|ExternalAuthAssertion|SecurityProof|SecurityLogin|SessionCookieOrigin|AccessToken|DashboardAccessToken)' -count=1
```

The email matrix uses `TEST_SECURITY_EMAIL_MYSQL_DSN` and
`TEST_SECURITY_EMAIL_POSTGRES_DSN`; use isolated local scratch databases only.
External cases skip without these variables. The acceptance run set both and
all three engines passed. The existing fresh/upgrade team matrix also passed.
No production database or SMTP service was used.

Frontend typechecking, focused lint, 74 frontend tests, production build and
root `go build ./...` passed. Playwright exercised both actions with a local
mock API at 1440px desktop and 375px mobile (light/dark), with no page errors or
horizontal overflow. Production email delivery remains a deployment acceptance
step; mock UI tests do not verify SMTP or financial payment processing.
