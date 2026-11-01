# How to use CronSoul

Created and maintained by **[@user4xn](https://github.com/user4xn)**.

This guide covers the full operator workflow in the CronSoul web console. The interface is served by the CronSoul instance; it does not require a hosted account. Labels below refer to the current English UI.

## 1. First login and account security

1. Start CronSoul and open its configured base URL.
2. Sign in as `admin` using the temporary password printed in the startup documentation (`12345678`).
3. Set a unique password of at least 10 characters when prompted. The initial password cannot be reused.
4. Create named users under **Users** and assign the least privilege needed:
   - `viewer`: read-only operational visibility.
   - `qa`: view and run endpoints.
   - `admin`: manage endpoints and users.
   - `superadmin`: manage security controls, allowlist, and notification channels.
5. Sign out from the profile control. Sessions are HTTP-only cookies and are invalidated when logging out.

Do not publish an instance with the initial credentials. Put the web console behind HTTPS before remote access.

## 2. Understand the workspace

The sidebar groups the console into:

- **Monitor:** Endpoints, Runs, Timeline, Alerts, Channels.
- **Governance:** Users, Allowlist, Audit log (some items are role restricted).
- **System:** Settings.

The environment badge identifies the running instance. It is not an environment switcher: deploy separate instances/databases for separate environments.

## 3. Configure outbound destinations first

CronSoul blocks outbound targets unless their host/port is allowed. Before creating a job:

1. Sign in as `superadmin` and open **Allowlist**.
2. Add the exact DNS host, optionally with a port (for example, `api.example.net` or `api.example.net:8443`).
3. Avoid broad wildcard patterns unless necessary; inspect the effective host/port carefully.
4. Private, loopback, link-local, multicast, and cloud metadata address ranges remain subject to mandatory blocks even if someone adds an allowlist entry.

CronSoul rejects redirects, applies request timeouts, and limits response size. Use HTTPS targets. Plain HTTP is only available in the explicitly time-limited test mode where supported.

## 4. Create an endpoint

1. Open **Endpoints** and choose **New endpoint**.
2. Give it a unique key, a readable name, and service label.
3. Set method and absolute URL. Add query parameters, headers, and an optional JSON body as required by the receiver.
4. Configure authentication using the supported auth type. Secrets are encrypted at rest and are not returned by read APIs.
5. Configure the five-field cron expression and timezone. Use the schedule description and preview to confirm the actual next fire times; do not infer timezone from the browser clock.
6. Set timeout and resilience policy. Keep retries conservative unless the receiver is idempotent. `Idempotency-Key`/run identity should be honored by the endpoint before retrying writes.
7. Add assertions only when their expected response behavior is clear. Assertions are reported separately from HTTP success.
8. Save the endpoint. Use **Test connection** or **Play** to validate behavior before enabling an operational schedule.

The form can import a cURL command. Review every parsed URL, header, authentication field, and body before saving; imported credentials are sensitive.

### Request placeholders and runtime headers

The UI documents supported template placeholders next to request fields. CronSoul also sends run-identifying headers such as `X-Run-Id`, `X-Trigger`, `X-Triggered-By`, and `Idempotency-Key`; simulated time is sent only when configured and selected. The receiver example demonstrates idempotency and simulated-time handling.

## 5. Run a job manually

- **Play** enqueues a manual execution; an accepted response means queued, not completed.
- A `409` indicates a singleton job already has an active run; wait for it to finish.
- Dry run is not a universal no-op: it adds the configured marker/query parameter and still calls the target. Use it only with a receiver that treats the marker safely.
- Simulated time requires a clock-aware receiver/header configuration. It changes the time supplied to the receiver; it does not change the host scheduler clock.

## 6. Diagnose Runs

Open **Runs** and select a row for detailed evidence:

- status, HTTP response code, duration, trigger, and attempts;
- request URL and configured request details;
- response excerpt, assertion results, and notification state where available;
- copy-cURL action for reproducing the request shape.

Treat copied commands as sensitive if they contain authentication material. Redact credentials before sharing logs or tickets. Use the attempt timeline to distinguish a final failure from transient attempts that later succeeded.

## 7. Use the Timeline

The **Timeline** view groups endpoint activity by hour over the selected period. Use it to locate clusters of failures or missed windows, then open the relevant lane/hour for its run history. The display timezone follows the configured timezone. A blank cell means no run was recorded for that endpoint/hour; it does not necessarily indicate that the service was healthy.

## 8. Alerts and notification channels

1. As `superadmin`, open **Channels** and configure SMTP or Telegram.
2. Select the event types the channel should receive.
3. Send a test notification and confirm the destination receives it.
4. Open **Alerts** and inspect the alert record and outbox delivery status.

A configured channel does not prove external delivery; verify it with a real recipient. Do not paste tokens or SMTP passwords into public issues.

## 9. Settings, users, and audit

- **Settings:** instance mode and runtime configuration (read-only), scheduler enablement, console timezone, insecure-HTTP test mode, per-browser notification duration, and links to this repository's documentation.
  - **Instance mode** reports `dev` or `production` as configured when the server started. It is not a switch: change `CRONSOUL_ENV` and restart the process. Production additionally requires an HTTPS `CRONSOUL_BASE_URL`, a file-backed `CRONSOUL_DB`, a stable key, and `CRONSOUL_TLS_MODE=proxy` or `cert`; startup refuses to run otherwise.
  - **Runtime configuration** is read-only: scheduler state, session lifetime, response body limit (shown in bytes and KiB, with a 1 MiB hard cap), private-network egress, temporary HTTP mode, and redirect policy.
  - **Notification duration** applies to the toast messages in this browser only; it is stored locally, not in the database.
  - **Running build** shows the served version and links to the repository, README, this guide, and the container/environment reference. Confirm the build there before reporting a stale page.
- **Users:** create accounts and assign minimum roles. Reset passwords only through the authorized admin flow; users must change temporary passwords.
- **Audit log:** review actor, action, target, and timestamp after governance changes. Keep the database and backups access-controlled because they include audit and password-hash data.

## 10. Operate safely

- Back up the SQLite database and encryption key together. Test restore before relying on backups.
- Use a persistent writable volume for `/data`; do not use an ephemeral container filesystem.
- Keep the web port private behind a TLS reverse proxy. The sample Compose binds loopback only.
- Rotate credentials at the upstream service, then update CronSoul's encrypted config. Changing the encryption key without re-entering channel secrets makes old encrypted configs unreadable.
- Monitor disk space, container restarts, DB integrity, and notification outbox failures.
- Upgrade from a backup and a tested release image; do not assume schema downgrade is supported.

## 11. Troubleshooting quick checks

- **Login returns 401:** verify username/password and account status; do not repeatedly retry (login is rate-limited).
- **API returns 428:** the account must complete the forced password change.
- **Run is refused:** check URL scheme, allowlisted host/port, DNS resolution, and protected IP-range restrictions.
- **Play returns 409:** an active singleton run exists.
- **No automatic runs:** check scheduler enablement, cron expression, timezone, process health, and whether the instance remains running.
- **Notifications are missing:** inspect channel event subscriptions, test-send result, outbox state, network egress, and provider credentials.
- **Channels cannot decrypt after restart:** verify the same `CRONSOUL_SECRET_KEY` or persisted key file is mounted with the database volume.
