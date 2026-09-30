---
title: "Peribolos"
weight: 10
description: >
  
---

Peribolos allows the org settings, teams and memberships to be declared in a yaml file. GitHub is then updated to match the declared configuration.

See the [kubernetes/org] repo, in particular the [merge] and [`update.sh`] parts of that repo for this tool in action.

Peribolos was the subject of a KubeCon talk: [How Kubernetes Uses GitOps to Manage GitHub Communities at Scale][kubecon talk]

### Etymology

A [peribolos] is a wall that encloses a court in Greek/Roman architecture.

## Org configuration

Extend the primary prow [`config.yaml`] document to include a top-level `orgs` key that looks like the following:

```yaml
orgs:
  this-org:
    # org settings
    company: foo
    email: foo
    name: foo
    description: foo
    has_organization_projects: true
    has_repository_projects: true
    default_repository_permission: read
    members_can_create_repositories: false

    # org member settings
    members:
    - anne
    - bob
    admins:
    - carl

    # team settings
    teams:
      node:
        # team config
        description: people working on node backend
        privacy: closed
        previously:
        - backend  # If a backend team exists, rename it to node

        # team members
        members:
        - anne
        maintainers:
        - jane
        repos: # Ensure the team has the following permissions levels on repos in the org
          some-repo: admin
          other-repo: read
      another-team:
        ...
      ...

    # organization role assignments (the custom org role must already exist in GitHub)
    roles:
      security_manager:
        teams:
        - node
        users:
        - anne
  that-org:
    ...
```

This config will:

* Ensure the org settings match the following:
  * Set the company, email, name and descriptions fields for the org to foo
  * Allow projects to be created at the org and repo levels
  * Give everyone read access to repos by default
  * Disallow members from creating repositories
* Ensure the following memberships exist:
  * anne and bob are members, carl is an admin
* Configure the node and another-team in the following manner:
  * Set node's description and privacy setting.
  * Rename the backend team to node
  * Add anne as a member and jane as a maintainer to node
  * Similar things for another-team (details elided)
* Ensure that the team has admin rights to `some-repo`, read access to `other-repo` and no other privileges
* Assign the `security_manager` organization role to the `node` team and directly to `anne`

Note that any fields missing from the config will not be managed by peribolos. So if description is missing from the org setting, the current value will remain.

For more details please see GitHub documentation around [edit org], [update org membership], [edit team], [update team membership].

### Organization roles

The `roles` key assigns [custom organization roles] to teams and users. Role management is opt-in and deliberately conservative:

* The role must already exist in GitHub. Peribolos assigns existing roles but never creates or deletes one; referencing a role that does not exist is a fatal error.
* Only roles declared in the config are managed. Roles absent from the config, including GitHub's built-in predefined roles and any managed out-of-band, are left untouched.
* To clear a role, declare it with no `teams`/`users`: an empty role removes all of its direct assignments.
* Indirect assignments are preserved. A team that only inherits a role from a parent team (or a user who holds it only through a team) is never removed.
* `--fix-org-roles` requires `--fix-teams`, because assignments are reconciled against the resolved team slugs.
* Teams excluded via `--ignore-secret-teams`/`--ignore-enterprise-teams` are left alone: an ignored team that holds a role keeps it.
* Roles are included in `--dump`. A dumped `roles` stanza is informational until you apply it with `--fix-org-roles`, at which point those roles become managed (and, per the rules above, a direct assignment that is not in the applied config would be removed).

### Initial seed

Peribolos can dump the current configuration to an org. For example you could dump the kubernetes org do the following:

```console
$ go run ./cmd/peribolos --dump kubernetes-sigs --github-token-path ~/github-token | tee ~/current.yaml
...
INFO: Build completed successfully, 1 total action
...
{"client":"github","component":"peribolos","level":"info","msg":"GetOrg(kubernetes-sigs)","time":"2018-09-28T13:17:42-07:00"}
{"client":"github","component":"peribolos","level":"info","msg":"ListOrgMembers(kubernetes-sigs, admin)","time":"2018-09-28T13:17:42-07:00"}
{"client":"github","component":"peribolos","level":"info","msg":"ListOrgMembers(kubernetes-sigs, member)","time":"2018-09-28T13:17:43-07:00"}
{"client":"github","component":"peribolos","level":"info","msg":"ListTeams(kubernetes-sigs)","time":"2018-09-28T13:17:45-07:00"}
{"client":"github","component":"peribolos","level":"info","msg":"ListTeamMembers(2671356, maintainer)","time":"2018-09-28T13:17:46-07:00"}
{"client":"github","component":"peribolos","level":"info","msg":"ListTeamMembers(2671356, member)","time":"2018-09-28T13:17:46-07:00"}
...
admins:
- calebamiles
- cblecker
- etc
billing_email: secret@example.com
company: ""
default_repository_permission: read
description: Org for Kubernetes SIG-related work
email: ""
has_organization_projects: true
has_repository_projects: true
location: ""
members:
- ameukam
- amwat
- ant31
- etc
teams:
  application-admins:
    description: admin access to application
    maintainers:
    - kow3ns
    members:
    - mattfarina
    - prydonius
    privacy: closed
  architecture-tracking-admins:
    description: admin permission for architecture-tracking
    maintainers:
    - jdumars
    - bgrant0607
    privacy: closed
  # etc
```

Open `~/current.yaml` and then delete any metadata you don't want peribolos to manage (such as billing_email, or all the teams, etc).

Apply this config in dry-run mode to see what would happen (hopefully nothing since you just created it):

```console
$ go run ./cmd/peribolos --config-path ~/current.yaml --github-token-path ~/github-token # --confirm

{"client":"github","component":"peribolos","level":"info","msg":"GetOrg(kubernetes-sigs)","time":"2018-09-27T23:07:13Z"}
{"client":"github","component":"peribolos","level":"info","msg":"ListOrgInvitations(kubernetes-sigs)","time":"2018-09-27T23:07:13Z"}
{"client":"github","component":"peribolos","level":"info","msg":"ListOrgMembers(kubernetes-sigs, admin)","time":"2018-09-27T23:07:13Z"}
{"client":"github","component":"peribolos","level":"info","msg":"ListOrgMembers(kubernetes-sigs, member)","time":"2018-09-27T23:07:14Z"}
...
```

## Settings

In order to mitigate the chance of applying erroneous configs, the peribolos binary includes a few safety checks:

* `--required-admins=` - a list of people who must be configured as admins in order to accept the config (defaults to empty list)
* `--min-admins=5` - the config must specify at least this many admins
* `--require-self=true` - require the bot applying the config to be an admin.

These flags are designed to ensure that any problems can be corrected by rerunning the tool with a fixed config and/or binary.

* `--maximum-removal-delta=0.25` - reject a config that deletes more than 25% of the current memberships.

This flag is designed to protect against typos in the configuration which might cause massive, unwanted deletions. Raising this value to 1.0 will allow deleting everyone, and reducing it to 0.0 will prevent any deletions.

* `--confirm=false` - no github mutations will be made until this flag is true. It is safe to run the binary without this flag. It will print what it would do, without actually making any changes.

See `go run ./cmd/peribolos --help` for the full and current list of settings that can be configured with flags.

[`config.yaml`]: https://github.com/kubernetes/test-infra/blob/master/config/prow/config.yaml
[edit team]: https://developer.github.com/v3/teams/#edit-team
[edit org]: https://developer.github.com/v3/orgs/#edit-an-organization
[peribolos]: https://en.wikipedia.org/wiki/Peribolos
[update org membership]: https://developer.github.com/v3/orgs/members/#add-or-update-organization-membership
[update team membership]: https://developer.github.com/v3/teams/members/#add-or-update-team-membership
[merge]: https://github.com/kubernetes/org/tree/master/cmd/merge
[kubernetes/org]: https://github.com/kubernetes/org
[`update.sh`]: https://github.com/kubernetes/org/blob/master/admin/update.sh
[kubecon talk]: https://www.youtube.com/watch?v=te3Xj2zr1Co
[custom organization roles]: https://docs.github.com/en/organizations/managing-peoples-access-to-your-organization-with-roles/about-custom-organization-roles
