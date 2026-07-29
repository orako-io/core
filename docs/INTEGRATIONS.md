# Messaging integrations

The dashboard Inbox always works. Messaging integrations are optional delivery
channels that let teammates receive and answer Orako questions where they
already work.

Provider credentials belong to the organization. Alert channels and member
bindings can vary by project or person.

## Supported setup flows

| Provider | Current status | Setup |
|---|---|---|
| Slack | Available | Guided admin flow in **Integrations → Slack** |
| Discord | Available | Guided admin flow in **Integrations → Discord** |
| Microsoft Teams | Adapter present, guided setup not released | Do not use for a new production deployment yet |
| Telegram | Adapter present, no guided dashboard setup | Advanced/manual use only |

The dashboard is the source of truth for the current setup steps. Provider
consoles change frequently, so follow the copyable URLs and permissions shown
inside Orako instead of an old screenshot.

## Slack

An organization admin creates a Slack app from the manifest shown by Orako,
installs it, then saves:

- the bot token (`xoxb-…`);
- the signing secret;
- the generated Slack Events request URL;
- optional alert channel IDs.

Each teammate needs a Slack member ID before Orako can send them a direct
message. The Team page can also synchronize IDs from the Slack directory when
the app has access.

Server-level `ORAKO_SLACK_CLIENT_ID`, `ORAKO_SLACK_CLIENT_SECRET`, and
`ORAKO_SLACK_SIGNING_SECRET` enable the optional OAuth installation shortcut.
The manual guided flow works with credentials entered in the dashboard.

## Discord

An organization admin creates a Discord application and bot, enables the
Message Content intent, adds the bot to the team's server, then saves the bot
token in Orako.

Each teammate must either:

- use the one-click Discord connection, when the admin configured the optional
  Discord OAuth client secret; or
- paste their Discord user ID manually.

Discord can deliver DMs only when the bot and teammate share a server and the
teammate permits direct messages from server members.

## Test before inviting the team

For every provider:

1. Complete the guided setup.
2. Bind the admin's own provider identity.
3. Use **Send test** from the integration page.
4. Confirm the direct message arrives.
5. Configure an optional alert channel and test an unanswered pool question.

Do not invite the full team until the direct-message test succeeds.

## Security

Provider credentials are encrypted at rest when `ORAKO_ENCRYPTION_KEY` is set.
The production Compose file requires this key. Keep it stable: changing it
makes stored provider credentials unreadable.

Only expose provider webhook routes through HTTPS. Slack requests are verified
with the signing secret, Discord inbound traffic uses the authenticated gateway,
and Teams verifies Microsoft-signed tokens.

## Troubleshooting

### The integration is connected but a teammate gets no DM

- Confirm the teammate has the provider ID saved on their profile.
- Confirm their selected delivery channel matches the provider.
- Send a provider test from the dashboard.
- For Discord, confirm the shared server and DM privacy setting.
- For Slack, confirm the bot remains installed in the workspace.

### Replies do not return to Orako

- Slack: verify the Events request URL, `message.im` subscription, and signing
  secret.
- Discord: verify the bot is online and Message Content intent remains enabled.
- Check the Orako server logs without logging or pasting provider credentials.

### Alert channels do not receive escalations

Alert channels are separate from direct-message delivery. Confirm the channel
ID is saved for the relevant project and the bot has permission to post there.
