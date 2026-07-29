// Provider catalogue driving the Integrations gallery + connect stepper +
// manage view. Credential keys match the server's requiredCredentials
// (core/internal/application/command/configure_provider.go). Step content is
// condensed from aidd_docs/content/provider-connect-guides.md.

export type ProviderKind = 'slack' | 'teams' | 'discord'

export interface CredentialField {
  key: string
  label: string
  placeholder: string
  // secret fields render masked once saved; all are monospace inputs.
  secret?: boolean
}

// A stepper step. `type` drives what the step renders:
//  - oauth:       the Slack "Connect" button (fast path) + manual expander
//  - console:     work happens in the provider console; Next only, no save
//  - credentials: the credential fields for this provider + a Save
//  - channels:    the alert-channels chips input + a Save
//  - test:        the "send a test ping" + success + per-teammate nudge
export interface Step {
  type: 'oauth' | 'console' | 'credentials' | 'channels' | 'test'
  title: string // rail title
  sub?: string // rail subtitle
  eyebrow?: string // "Why" / "Do this" heading in the content
  heading: string // the big content heading
  why?: string // the "why" paragraph
  doThis?: string // the "Do this" instructions (may contain simple markup words)
  // console steps may show a copy-box URL Orako gives you:
  copyUrl?: (origin: string, projectId: string) => string
  copyLabel?: string
  // Multiline copy box (e.g. a Slack app manifest) — rendered as a <pre>.
  copyBlock?: (origin: string, projectId: string) => string
  copyBlockLabel?: string
  note?: string
  // A deep link to the provider's console, so the user doesn't have to search.
  link?: { label: string; url: string }
  // Extra deep links rendered as buttons alongside `link` (e.g. a docs page).
  links?: { label: string; url: string }[]
  // Ordered sub-steps rendered as a numbered list — clearer than one paragraph
  // for multi-click console work. Rendered after `doThis`.
  bullets?: string[]
  // Custom label for the console step's "Next" button (e.g. an explicit
  // confirmation like "I've added the bot to my server").
  nextLabel?: string
  // botInvite: render a one-click "Add the bot to your server" button whose URL
  // is derived from the saved Discord bot token (client_id decoded from it), so
  // the admin never touches the OAuth2 URL Generator by hand.
  botInvite?: boolean
  // bindSelfDiscord: render an inline "Your Discord user ID" field that binds the
  // signed-in admin's own id, so the DM test reaches them without leaving the flow.
  bindSelfDiscord?: boolean
  // credentialKeys narrows which of the provider's credentialFields this step
  // renders (and, on a console step, adds those inputs + a Save). Absent = every
  // field (the default for a plain credentials step). Lets one provider split its
  // credentials across steps — e.g. Discord's bot token in step 1 and its
  // optional OAuth2 client secret in the later "1-click connect" step — while a
  // single shared credValues map still persists them together on Save.
  credentialKeys?: string[]
}

export interface ProviderDef {
  kind: ProviderKind
  label: string
  timeLabel: string // "~10 min"
  who: string // "Slack admin"
  // Slack has an OAuth fast path; the others are token/credential based.
  oauth?: boolean
  // Brand color for the primary action button in the stepper (e.g. Slack aubergine).
  brand?: string
  credentialFields: CredentialField[]
  steps: Step[]
  // A one-line "good to know" reassurance shown at the bottom of the rail.
  reassurance: string
  // comingSoon hides the connect entry point (shown greyed with a badge):
  // the integration exists in code but is not ready for users yet.
  comingSoon?: boolean
}

// slackManifest is the app manifest the admin pastes at api.slack.com — it
// pre-declares the bot and every scope Orako needs. Event Subscriptions are
// deliberately NOT in the manifest: Slack verifies the Request URL against
// Orako's signature check, which needs the Signing Secret saved in Orako
// FIRST — so events are enabled as a later step, after credentials are saved.
function slackManifest(): string {
  return JSON.stringify(
    {
      display_information: {
        name: 'Orako',
        description: 'Routes questions to teammates and keeps every answer in searchable conversation history.',
        background_color: '#1D2430',
      },
      features: {
        bot_user: { display_name: 'Orako', always_online: true },
      },
      oauth_config: {
        scopes: { bot: ['chat:write', 'im:write', 'users:read'] },
      },
      settings: {
        org_deploy_enabled: false,
        socket_mode_enabled: false,
        token_rotation_enabled: false,
      },
    },
    null,
    2,
  )
}

export const PROVIDERS: Record<ProviderKind, ProviderDef> = {
  slack: {
    kind: 'slack',
    label: 'Slack',
    timeLabel: '~10 min',
    who: 'Slack admin',
    brand: '#4A154B',
    reassurance:
      'Orako only DMs teammates and reads the reply to its own message — it can’t see the rest of your Slack.',
    credentialFields: [
      { key: 'bot_token', label: 'Bot token', placeholder: 'xoxb-…', secret: true },
      { key: 'signing_secret', label: 'Signing secret', placeholder: 'from Basic Information → App Credentials', secret: true },
    ],
    steps: [
      {
        type: 'console',
        title: 'Create the Slack app',
        sub: 'From Orako’s manifest',
        eyebrow: 'Do this',
        heading: 'Create the app from Orako’s manifest',
        why: 'A manifest pre-declares the bot and every permission Orako needs (chat:write, im:write, users:read) — nothing to pick by hand, no scope to forget.',
        doThis: 'In the Slack API console:',
        bullets: [
          'Click “Create New App” → “From a manifest”.',
          'Pick your team’s workspace.',
          'Switch the format to JSON, replace the sample with the manifest below, click “Next” then “Create”.',
        ],
        copyBlockLabel: 'App manifest',
        copyBlock: () => slackManifest(),
        link: { label: 'Open Slack API · Your Apps', url: 'https://api.slack.com/apps' },
        nextLabel: 'App created →',
      },
      {
        type: 'credentials',
        title: 'Install & paste credentials',
        sub: 'Token + signing secret',
        eyebrow: 'Do this',
        heading: 'Install the app and paste its credentials',
        why: 'The Bot token lets Orako DM your teammates; the Signing secret lets it verify that inbound replies really come from Slack. Save them here BEFORE the next step — Slack’s URL verification depends on it.',
        doThis: 'On your new app’s page:',
        bullets: [
          'Left sidebar → “Install App” → “Install to Workspace” → Allow.',
          '“OAuth & Permissions” → copy the “Bot User OAuth Token” (starts with xoxb-) into the first field below.',
          '“Basic Information” → “App Credentials” → “Signing Secret” → Show → copy it into the second field.',
          'Click Save.',
        ],
      },
      {
        type: 'console',
        title: 'Enable Event Subscriptions',
        sub: 'Replies flow back',
        eyebrow: 'Do this',
        heading: 'Point Slack’s events at Orako',
        why: 'This is how teammates’ replies reach Orako. Slack verifies the URL by challenging Orako with your Signing secret — which is why the previous step had to be saved first.',
        doThis: 'On the app’s page:',
        bullets: [
          'Left sidebar → “Event Subscriptions” → toggle “Enable Events” ON.',
          'Paste the Request URL below — it must show “Verified ✓” (if it errors, re-check the Signing secret you saved in the previous step).',
          'Under “Subscribe to bot events”, click “Add Bot User Event” and add message.im.',
          'Click “Save Changes” (bottom right). Slack may ask to reinstall the app — accept.',
        ],
        copyLabel: 'Request URL',
        copyUrl: (origin, projectId) => `${origin}/slack/events/${projectId}`,
        nextLabel: 'Events verified →',
      },
      {
        type: 'console',
        title: 'Reach each teammate',
        sub: 'Member IDs',
        eyebrow: 'Do this',
        heading: 'Let Orako DM you and your teammates',
        why: 'Orako DMs a person by their Slack member ID. Bind each teammate once on the Team page so questions reach them.',
        doThis: 'Get each person’s Slack member ID:',
        bullets: [
          'In Slack, click the person’s profile picture → “View full profile”.',
          'Click the “⋮” (three dots) → “Copy member ID” (starts with U).',
          'Paste it on the person’s card in Orako’s Team page (Slack user ID field).',
        ],
        links: [{ label: 'Set teammates’ IDs in Team', url: '/members' }],
      },
      {
        type: 'channels',
        title: 'Alert channels',
        sub: 'Optional · then test',
        heading: 'Where escalations post',
        why: 'If a pool question goes unanswered, Orako posts once to these channels. Optional — leave empty to skip, then send a test.',
        doThis: 'Paste one or more channel IDs (comma-separated, right-click the channel → “Copy link” — the ID is the last path segment, starts with C). Invite the bot to each channel first: /invite @Orako.',
      },
    ],
  },
  teams: {
    kind: 'teams',
    label: 'Microsoft Teams',
    timeLabel: '~25 min',
    who: 'Azure admin',
    comingSoon: true,
    brand: '#5059C9',
    reassurance:
      'Orako verifies a Microsoft-signed token on every incoming reply — only genuine Teams traffic is accepted.',
    credentialFields: [
      { key: 'tenant_id', label: 'Directory (tenant) ID', placeholder: '00000000-0000-…' },
      { key: 'client_id', label: 'Application (client) ID', placeholder: '00000000-0000-…' },
      { key: 'client_secret', label: 'Client secret', placeholder: 'secret value', secret: true },
      { key: 'bot_app_id', label: 'Bot app ID', placeholder: 'bot registration app id' },
    ],
    steps: [
      {
        type: 'credentials',
        title: 'Register the app + credentials',
        sub: 'Azure · four fields',
        eyebrow: 'Do this',
        heading: 'Register the Orako app in Azure, then paste its credentials',
        why: 'Teams delivers via a registered app your tenant approves. This is the heaviest of the three — set aside ~25 minutes.',
        doThis:
          'In Microsoft Entra ID → App registrations → New registration, create an app named "Orako." Add a client secret. Copy the Application (client) ID, Directory (tenant) ID, the client secret value, and the bot app ID, then paste all four below and Save.',
        link: { label: 'Open the Azure portal · App registrations', url: 'https://portal.azure.com/#view/Microsoft_AAD_RegisteredApps/ApplicationsListBlade' },
      },
      {
        type: 'console',
        title: 'Point Teams back + approve',
        sub: 'Endpoint · admin consent',
        heading: 'Set the messaging endpoint, then approve the app',
        why: 'This is where Teams delivers replies (Orako verifies a Microsoft-signed token on each). Tenant admin consent is what lets Orako start a chat with a person proactively.',
        doThis:
          'Set your bot’s messaging endpoint to the URL below. Then sideload the Orako Teams app so members can receive messages, and have a tenant admin approve it.',
        copyUrl: origin => `${origin}/teams/messages`,
        copyLabel: 'Messaging endpoint',
        link: { label: 'Open the Bot Framework / Azure Bot', url: 'https://portal.azure.com/#view/Microsoft_AAD_RegisteredApps/ApplicationsListBlade' },
      },
      {
        type: 'channels',
        title: 'Alert channels',
        sub: 'Optional · then test',
        heading: 'Where escalations post',
        why: 'Optional channels Orako posts to when a pool question goes unanswered — then send a test ping.',
        doThis: 'Add one or more channel IDs (comma-separated), or leave empty.',
      },
    ],
  },
  discord: {
    kind: 'discord',
    label: 'Discord',
    timeLabel: '~10 min',
    who: 'Server admin',
    brand: '#5865F2',
    reassurance:
      'Orako connects out to Discord and only messages teammates who share its server — nothing public is exposed. Conversation threads are private; only invited members see them.',
    credentialFields: [
      { key: 'bot_token', label: 'Bot token', placeholder: 'paste the token you just copied', secret: true },
      { key: 'client_secret', label: 'OAuth2 client secret (optional)', placeholder: 'enables 1-click “Connect Discord” for teammates', secret: true },
    ],
    steps: [
      {
        type: 'credentials',
        title: 'Create the bot & paste its token',
        sub: 'Developer Portal',
        eyebrow: 'Do this',
        heading: 'Create the bot, copy its token, and paste it here',
        why: 'Orako DMs your teammates through a Discord bot you own. Discord shows the token only once — so copy it and paste it right here before moving on.',
        doThis: 'Follow these in the Developer Portal, then paste the token in the field below and Save:',
        credentialKeys: ['bot_token'],
        bullets: [
          'Click “New Application”, name it “Orako”, accept the terms.',
          'Open “Bot” in the left sidebar.',
          'Under “Token”, click “Reset Token”, confirm, and copy the value.',
          'Under “Privileged Gateway Intents”, turn ON “Message Content Intent” (Orako reads messages typed in its own conversation threads; the other two stay OFF).',
          'Come back here and paste the token below, then Save (you can’t get it again later).',
        ],
        link: { label: 'Open the Discord Developer Portal', url: 'https://discord.com/developers/applications' },
      },
      {
        type: 'console',
        title: 'Add the bot to your server',
        sub: 'One click',
        eyebrow: 'Do this',
        heading: 'Add the bot to the server your team is in',
        why: 'This is the step most people miss: Discord only lets a bot DM people it shares a server with, so the bot must be a member of your team’s server (the same one your alert channel lives in). The invite also grants the thread permissions Orako uses to open a private discussion thread per question.',
        doThis: 'Click the button below — it opens Discord with the bot ready to add. Pick your team’s server and click “Authorize”. That’s it.',
        botInvite: true,
        nextLabel: 'I’ve added the bot to my server →',
        note: 'The bot needs no admin rights. If you added it to the wrong server, click the button again and pick the right one.',
      },
      {
        type: 'console',
        title: 'Enable 1-click connect',
        sub: 'OAuth2 · optional',
        eyebrow: 'Do this',
        heading: 'Let teammates link Discord in one click (optional)',
        why: 'Without this, each teammate copies their 18-digit Discord user ID by hand (next step). Register an OAuth2 redirect + secret once and they just click “Connect Discord” instead — no ID to find, nothing to paste.',
        doThis: 'In the Developer Portal → your app → OAuth2:',
        credentialKeys: ['client_secret'],
        bullets: [
          'Under “Redirects”, add the callback URL below EXACTLY, then “Save Changes”.',
          'On the same page, under “Client Secret”, click “Reset Secret” and copy it.',
          'Paste it into the “OAuth2 client secret” field below and Save.',
        ],
        copyUrl: origin => origin + '/discord/oauth/callback',
        copyLabel: 'Redirect URL — paste into Discord → OAuth2 → Redirects',
        link: { label: 'Open Discord Developer Portal → OAuth2', url: 'https://discord.com/developers/applications' },
        note: 'Optional: skip it and teammates paste their ID manually in the next step. Bot delivery (DMs, private threads) works either way — this only adds the 1-click self-bind.',
      },
      {
        type: 'console',
        title: 'Reach each teammate',
        sub: 'Your ID · allow DMs',
        eyebrow: 'Do this',
        heading: 'Let Orako DM you and your teammates',
        why: 'Orako DMs a person by their Discord user ID, and Discord only delivers it if they allow DMs from server members. Bind yourself here so the test in the last step reaches you; do the same for each teammate on the Team page.',
        doThis: 'Get your Discord user ID and paste it below:',
        bullets: [
          'Discord → User Settings → “Advanced” → turn ON “Developer Mode”.',
          'Click your own name (or a teammate’s) and choose “Copy User ID”.',
          'Paste it below and Save. In Discord, also turn ON User Settings → “Privacy & Safety” → “Allow direct messages from server members”.',
        ],
        bindSelfDiscord: true,
        links: [{ label: 'Set teammates’ IDs in Team', url: '/members' }],
      },
      {
        type: 'channels',
        title: 'Alert channels',
        sub: 'Optional · then test',
        heading: 'Where escalations post',
        why: 'Where Orako posts when a pool question goes unanswered, and where it opens a private thread per question. Optional — leave empty to skip, then send a test.',
        doThis: 'Paste one or more channel IDs (comma-separated), or leave empty. The bot needs “View Channel”, “Send Messages”, “Create Private Threads”, “Send Messages in Threads”, “Manage Threads” and “Manage Webhooks” there — granted by the invite in step 2; for a private channel, add the bot to it.',
        note: 'Best practice: pick a channel the WHOLE team can see (e.g. #orako-questions). Discord only lets a private thread invite people who can view its parent channel. A teammate who can’t see the channel still gets the conversation by DM.',
      },
    ],
  },
}

export const PROVIDER_ORDER: ProviderKind[] = ['slack', 'teams', 'discord']

export function isProviderKind(k: string): k is ProviderKind {
  return k === 'slack' || k === 'teams' || k === 'discord'
}
