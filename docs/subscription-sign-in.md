# Subscription sign-in (OAuth): what each provider allows

Checked on 8 November 2026 for phase 9 of the pi parity plan. The rule for hm: build sign-in with a subscription only where the provider's own terms let a third-party tool use it. Terms change; check the linked pages again before building anything here.

This is about paying for model calls with a consumer or developer subscription (Claude Pro/Max, ChatGPT Plus/Pro, Google AI Pro/Ultra, Copilot) instead of an API key. It is not about the cloud credentials hm already uses: Vertex AI's application default credentials and Azure keys bill the user's own cloud account and are the supported way for third-party tools.

| Provider | Third-party use of the subscription | hm |
|---|---|---|
| Anthropic (Claude Free, Pro, Max, Ultra) | Not allowed | Not built |
| Google (Gemini CLI / Code Assist OAuth, Antigravity, Google AI Pro/Ultra) | Not allowed | Not built |
| OpenAI (ChatGPT Plus, Pro) | Allowed through OpenAI's "Sign in with ChatGPT" program for registered open-source clients, on the Responses API | Not built yet; possible, see below |
| GitHub Copilot | Officially supported for named partners (OpenCode, Zed) and apps built on the Copilot SDK with their own GitHub OAuth app; unclear for anything else | Not built |

## Anthropic: not allowed

Claude Code's legal and compliance page says OAuth "is intended exclusively for purchasers of Claude Free, Pro, Max, Ultra, Team, and Enterprise subscription plans and is designed to support ordinary use of Claude Code and other native Anthropic applications", and that "Anthropic does not permit third-party developers to offer Claude.ai login into their own applications, or to route requests through Free, Pro, Max, or Ultra plan credentials on behalf of their users." Developers "should use API key authentication through Claude Console or a supported cloud provider." Press coverage reports enforcement against third-party clients since January 2026.

hm keeps what it has: an API key, or the credentials the Anthropic SDK finds itself (`ANTHROPIC_API_KEY`, an `ant auth login` profile for the Claude API).

## Google: not allowed

The Gemini CLI terms page says: "Directly accessing the services powering Gemini CLI (for example, the Gemini Code Assist service) using third-party software, tools, or services (for example, using OpenClaw with Gemini CLI OAuth) is a violation of applicable terms and policies. Such actions may be grounds for suspension or termination of your account." Google suspended accounts for this in early 2026, paying Ultra subscribers included, and moved consumer plans from Gemini CLI to Antigravity in June 2026; Antigravity's terms forbid third-party tools and proxies too.

hm keeps the supported routes: a Gemini API key, or Vertex AI with Google Cloud credentials (phase 9C).

## OpenAI: allowed for registered open-source clients

OpenAI's Sign in with ChatGPT quickstart says eligible ChatGPT Plus and Pro users can use their plan for AI requests in participating apps, and that plan usage "is available to all open-source partners and selected private clients." The open-source flow registers the client and issues OAuth credentials (OpenID Connect with PKCE) whose scopes allow Responses API requests; it gives no access to the user's API key or conversations. Users can cap each app's weekly use, and requests draw on the plan's Codex allowance.

Building it would take:

1. Registering hm as an open-source client with OpenAI (the repository owner does this; it is not something the code can do).
2. A Responses API adapter: hm's OpenAI adapter speaks Chat Completions, and plan tokens are only good for the Responses API.
3. The sign-in flow (a browser, a local callback, PKCE), keeping the tokens in the keychain like API keys, and refreshing them.

Reusing Codex CLI's own client ID, as some tools do, is not the registered route and should not be done.

## GitHub Copilot: unclear, not built

GitHub officially supports using a Copilot subscription with OpenCode (changelog, 16 January 2026) and Zed (a formal partnership, February 2026), and documents the Copilot SDK, where you create your own GitHub OAuth app, users authorize it, and you pass their token to the SDK. Nothing found approves other clients that call `api.githubcopilot.com` with VS Code's OAuth client ID, which is how most third-party Copilot integrations work; GitHub's API terms allow suspension for abuse at its discretion. The Copilot product terms were replaced by the GitHub Generative AI Services Terms in March 2026, which I could not read in full.

A supported route would be the Copilot SDK with hm's own GitHub OAuth app. That needs checking whether the SDK can be used from Go, and the new terms read in full, before building.

## Sources

- Anthropic: [Claude Code legal and compliance](https://code.claude.com/docs/en/legal-and-compliance); [The Register, 20 Feb 2026](https://www.theregister.com/software/2026/02/20/anthropic-clarifies-ban-on-third-party-tool-access-to-claude/5014546); [WinBuzzer, 19 Feb 2026](https://winbuzzer.com/2026/02/19/anthropic-bans-claude-subscription-oauth-in-third-party-apps-xcxwbn/)
- Google: [Gemini CLI terms of service and privacy](https://geminicli.com/docs/resources/tos-privacy/); [Gemini CLI FAQ](https://geminicli.com/docs/resources/faq/); [Antigravity bans discussion](https://github.com/google-gemini/gemini-cli/discussions/20632); [service update discussion](https://github.com/google-gemini/gemini-cli/discussions/22970)
- OpenAI: [Sign in with ChatGPT quickstart](https://developers.openai.com/siwc/quickstart); [Codex authentication](https://developers.openai.com/codex/auth); [The New Stack](https://thenewstack.io/sign-in-with-chatgpt/)
- GitHub: [Copilot supports OpenCode](https://github.blog/changelog/2026-01-16-github-copilot-now-supports-opencode/); [Copilot in Zed](https://github.blog/changelog/2026-02-19-github-copilot-support-in-zed-generally-available/); [Copilot SDK with GitHub OAuth](https://docs.github.com/en/copilot/how-tos/copilot-sdk/setup/github-oauth); [GitHub API terms](https://docs.github.com/en/site-policy/github-terms/github-terms-of-service#h-api-terms); [community research note](https://github.com/djosh34/copilot-cli/issues/5)
