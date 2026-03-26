# Tiltfile - Prow local development inner loop.
#
# Tilt watches for source file changes, rebuilds affected component images, and
# rolls them out to the running kind cluster automatically.
#
# Prerequisite: the kind cluster must already be running. If this is your first
# time, run 'make dev' once to create the cluster and deploy the core components.
# After that, use 'tilt up' for the fast rebuild/redeploy loop.
#
# See site/content/en/docs/local-dev-tilt.md for full documentation.

version_settings(constraint='>=0.33.0')

allow_k8s_contexts('kind-kind-prow-integration')

REGISTRY    = 'localhost:5001'
CLUSTER_CFG = 'test/integration/config/prow/cluster/'

# ---------------------------------------------------------------------------
# Settings - read from tilt-settings.yaml if present (git-ignored).
# Create or edit that file to override defaults without touching the Tiltfile.
# ---------------------------------------------------------------------------
settings = read_yaml('tilt-settings.yaml', default={})

trigger_mode_setting = settings.get('trigger_mode', 'auto')
if trigger_mode_setting == 'manual':
    trigger_mode(TRIGGER_MODE_MANUAL)

extra_components = settings.get('extra_components', [])

# ---------------------------------------------------------------------------
# Component definitions: name -> (source_dirs, deploy_yaml_files)
#
# source_dirs   - watched for file changes; relative to repo root.
# deploy_yamls  - applied by Tilt (service + deployment); relative to
#                 CLUSTER_CFG. RBAC is intentionally omitted here because it
#                 is static and already applied by 'make dev'.
# ---------------------------------------------------------------------------
COMPONENT_DEFS = {
    # Core fakes
    'fakeghserver':  (
        ['test/integration/cmd/fakeghserver/'],
        ['fakeghserver.yaml'],
    ),
    'fakegcsserver': (
        ['test/integration/cmd/fakegcsserver/'],
        ['fakegcsserver.yaml'],
    ),
    'fakegitserver': (
        ['test/integration/cmd/fakegitserver/'],
        ['fakegitserver.yaml'],
    ),
    # Core Prow services
    'horologium': (
        ['cmd/horologium/', 'pkg/'],
        ['horologium_service.yaml', 'horologium_deployment.yaml'],
    ),
    'prow-controller-manager': (
        ['cmd/prow-controller-manager/', 'pkg/'],
        ['prow_controller_manager_service.yaml', 'prow_controller_manager_deployment.yaml'],
    ),
    'sinker': (
        ['cmd/sinker/', 'pkg/'],
        ['sinker_service.yaml', 'sinker.yaml'],
    ),
    'hook': (
        ['cmd/hook/', 'pkg/'],
        ['hook_service.yaml', 'hook_deployment.yaml'],
    ),
    'crier': (
        ['cmd/crier/', 'pkg/'],
        ['crier_service.yaml', 'crier_deployment.yaml'],
    ),
    # Full-profile extras (requires 'make dev-full' before 'tilt up')
    'tide': (
        ['cmd/tide/', 'pkg/'],
        ['tide_service.yaml', 'tide_deployment.yaml'],
    ),
    'gerrit': (
        ['cmd/gerrit/', 'pkg/'],
        ['gerrit.yaml'],
    ),
    'gangway': (
        ['cmd/gangway/', 'pkg/'],
        ['gangway_service.yaml', 'gangway_deployment.yaml'],
    ),
    'moonraker': (
        ['cmd/moonraker/', 'pkg/'],
        ['moonraker_service.yaml', 'moonraker_deployment.yaml'],
    ),
    'pipeline': (
        ['cmd/pipeline/', 'pkg/'],
        ['pipeline_service.yaml', 'pipeline_deployment.yaml'],
    ),
    'sub': (
        ['cmd/sub/', 'pkg/'],
        ['sub.yaml'],
    ),
    'webhook-server': (
        ['cmd/webhook-server/', 'pkg/'],
        ['webhook_server_service.yaml', 'webhook_server_deployment.yaml'],
    ),
    'fakegerritserver': (
        ['test/integration/cmd/fakegerritserver/'],
        ['fakegerritserver.yaml'],
    ),
    'fakepubsub': (
        ['test/integration/cmd/fakepubsub/'],
        ['fakepubsub.yaml'],
    ),
}

# ---------------------------------------------------------------------------
# Prow config - re-applied automatically when config files change
# ---------------------------------------------------------------------------
local_resource(
    'prow-config',
    'hack/tilt-apply-config.sh',
    deps=[
        'test/integration/config/prow/config.yaml',
        'test/integration/config/prow/plugins.yaml',
        'test/integration/config/prow/jobs/',
    ],
    labels=['config'],
)

# ---------------------------------------------------------------------------
# Component images and deployments
#
# custom_build tells Tilt how to rebuild an image when source files change.
# k8s_yaml registers the deployment so Tilt can update it after each build.
# ---------------------------------------------------------------------------
def prow_component(name):
    if name not in COMPONENT_DEFS:
        fail('Unknown component "' + name + '". Add it to COMPONENT_DEFS in the Tiltfile.')
    src_dirs, yaml_files = COMPONENT_DEFS[name]
    custom_build(
        REGISTRY + '/' + name,
        'hack/tilt-build.sh ' + name + ' $EXPECTED_REF',
        src_dirs,
    )
    k8s_yaml([CLUSTER_CFG + f for f in yaml_files])
    k8s_resource(workload=name, labels=['prow'])

# Core profile (matches 'make dev' / 'hack/dev-env.sh -profile=core')
prow_component('fakeghserver')
prow_component('fakegcsserver')
prow_component('fakegitserver')
prow_component('horologium')
prow_component('prow-controller-manager')
prow_component('sinker')
prow_component('hook')
prow_component('crier')

# deck and deck-tenanted share one image; register the workload for each.
custom_build(
    REGISTRY + '/deck',
    'hack/tilt-build.sh deck $EXPECTED_REF',
    ['cmd/deck/', 'pkg/'],
)
k8s_yaml([
    CLUSTER_CFG + 'deck_service.yaml',
    CLUSTER_CFG + 'deck_deployment.yaml',
    CLUSTER_CFG + 'deck_tenant_deployment.yaml',
])
k8s_resource(workload='deck',         labels=['prow'])
k8s_resource(workload='deck-tenanted', labels=['prow'])

# Extra components from tilt-settings.yaml (requires 'make dev-full' first)
for component in extra_components:
    prow_component(component)

# ---------------------------------------------------------------------------
# Personal Tiltfile overrides - load any *.tiltfile in tilt.d/ (git-ignored).
# Use these for local customization without modifying the committed Tiltfile.
# ---------------------------------------------------------------------------
for f in listdir('tilt.d/'):
    if f.endswith('.tiltfile'):
        include('tilt.d/' + f)
