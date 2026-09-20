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
the enable switch, reward type, separate credit/cash percentages, holding period,
and minimum cash withdrawal. Percentages have two-decimal precision.

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
plaintext account. Binding, withdrawal requests, payout disclosure, and review
require existing 2FA or passkey verification bound to the current session.

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

This change adds five feature tables, order snapshot columns and the user credit
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

## Local Verification

The focused Go tests for team routes, payment callbacks, reward settlement,
withdrawals and quota/auth caches pass, as does the root application build.
Frontend verification includes 19 focused tests, scoped lint, a production
build, and isolated desktop/mobile/empty-state browser checks. The browser
checks use mocked local API data and do not access production services.

The repository-wide checks are not fully green: five backend test failures
were independently reproduced at the original HEAD, and six existing keys-
module TypeScript errors remain. These unrelated issues were not changed.
No live MySQL/PostgreSQL migration or real-money payment was exercised. No
commit, push or production deployment is included in this local verification.
