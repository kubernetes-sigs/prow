# Local Hook development folder

This folder contains configuration files to run [Hook](/docs/components/core/hook/) locally for plugin development and testing. This is a safe place to store sensible credentials, as the `config/local` folder is included in the gitignore.

For the full step-by-step guide, see the [Hook plugin development guide](/docs/getting-started-hook).

## Files

| File                             | Purpose                                                                                                                  |
| -------------------------------- | ------------------------------------------------------------------------------------------------------------------------ |
| **config.yaml**                  | Prow config for Hook.                                                                                                    |
| **plugins.yaml**                 | Which plugins run per org/repo. Replace `<GITHUB_USER>` with your GitHub username                                        |
| **hmac-secret**                  | GitHub webhook secret. Must match the **Secret** you set in your repo’s webhook (e.g. `abcde12345` as in the dev guide). |
| **prow-sandbox.private-key.pem** | Private key for your GitHub App. Generate from the App settings and place here so Hook can authenticate to GitHub.       |
