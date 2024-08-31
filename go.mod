module sigs.k8s.io/prow

go 1.22.3

require (
	cloud.google.com/go/cloudbuild v1.16.6
	cloud.google.com/go/pubsub v1.42.0
	cloud.google.com/go/secretmanager v1.13.6
	cloud.google.com/go/storage v1.43.0
	github.com/GoogleCloudPlatform/testgrid v0.0.123
	github.com/NYTimes/gziphandler v1.1.1
	github.com/andygrunwald/go-gerrit v0.0.0-20210709065208-9d38b0be0268
	github.com/andygrunwald/go-jira v1.14.0
	github.com/aws/aws-sdk-go v1.38.49
	github.com/bazelbuild/buildtools v0.0.0-20200922170545-10384511ce98
	github.com/blang/semver/v4 v4.0.0
	github.com/bombsimon/logrusr/v4 v4.1.0
	github.com/bwmarrin/snowflake v0.0.0
	// Upstream is unmaintained. This fork introduces two important changes:
	// * We log an error if writing a cache key fails (e.g. because disk is full)
	// * We inject a header that allows ghproxy to detect if the response was revalidated or a cache miss
	github.com/cjwagner/httpcache v0.0.0-20230907212505-d4841bbad466
	github.com/clarketm/json v1.13.4
	github.com/denormal/go-gitignore v0.0.0-20180930084346-ae8ad1d07817
	github.com/dgrijalva/jwt-go/v4 v4.0.0-preview1
	github.com/djherbis/atime v1.0.0
	github.com/evanphx/json-patch v5.9.0+incompatible
	github.com/felixge/fgprof v0.9.1
	github.com/fsnotify/fsnotify v1.7.0
	github.com/fsouza/fake-gcs-server v1.19.4
	github.com/go-git/go-git/v5 v5.12.0
	github.com/go-test/deep v1.0.7
	github.com/golang/glog v1.2.1
	github.com/gomodule/redigo v1.8.5
	github.com/google/go-cmp v0.6.0
	github.com/google/gofuzz v1.2.1-0.20210504230335-f78f29fc09ea
	github.com/google/uuid v1.6.0
	github.com/gorilla/csrf v1.6.2
	github.com/gorilla/mux v1.8.0
	github.com/gorilla/securecookie v1.1.1
	github.com/gorilla/sessions v1.2.0
	github.com/grpc-ecosystem/go-grpc-prometheus v1.2.0
	github.com/hashicorp/go-retryablehttp v0.7.6
	github.com/hashicorp/golang-lru v1.0.2
	github.com/mattn/go-zglob v0.0.2
	github.com/maxbrunsfeld/counterfeiter/v6 v6.4.1
	github.com/prometheus/client_golang v1.20.2
	github.com/prometheus/client_model v0.6.1
	github.com/prometheus/common v0.57.0
	github.com/shurcooL/githubv4 v0.0.0-20210725200734-83ba7b4c9228
	github.com/sirupsen/logrus v1.9.3
	github.com/spf13/pflag v1.0.5
	github.com/stretchr/testify v1.9.0
	github.com/tektoncd/pipeline v0.63.0
	go.uber.org/zap v1.27.0
	go4.org v0.0.0-20201209231011-d4a079459e60
	gocloud.dev v0.19.0
	golang.org/x/lint v0.0.0-20210508222113-6edffad5e616
	golang.org/x/net v0.28.0
	golang.org/x/oauth2 v0.22.0
	golang.org/x/sync v0.8.0
	golang.org/x/text v0.17.0
	golang.org/x/time v0.6.0
	gomodules.xyz/jsonpatch/v2 v2.4.0
	google.golang.org/api v0.195.0
	google.golang.org/genproto v0.0.0-20240823204242-4ba0660f739c
	google.golang.org/genproto/googleapis/api v0.0.0-20240827150818-7e3bb234dfed
	google.golang.org/grpc v1.66.0
	google.golang.org/protobuf v1.34.2
	gopkg.in/fsnotify.v1 v1.4.7
	gopkg.in/ini.v1 v1.62.0
	gopkg.in/robfig/cron.v2 v2.0.0-20150107220207-be2e0b0deed5
	gopkg.in/yaml.v3 v3.0.1
	k8s.io/api v0.31.0
	k8s.io/apimachinery v0.31.0
	k8s.io/client-go v0.31.0
	k8s.io/utils v0.0.0-20240821151609-f90d01438635
	knative.dev/pkg v0.0.0-20240815051656-89743d9bbf7c
	sigs.k8s.io/controller-runtime v0.19.0
	sigs.k8s.io/yaml v1.4.0
)

require (
	bitbucket.org/creachadair/stringset v0.0.9 // indirect
	cloud.google.com/go v0.115.1 // indirect
	cloud.google.com/go/compute/metadata v0.5.0 // indirect
	cloud.google.com/go/iam v1.1.13 // indirect
	cloud.google.com/go/longrunning v0.5.12 // indirect
	contrib.go.opencensus.io/exporter/ocagent v0.7.1-0.20200907061046-05415f1de66d // indirect
	contrib.go.opencensus.io/exporter/prometheus v0.4.2 // indirect
	github.com/Microsoft/go-winio v0.6.2 // indirect
	github.com/ProtonMail/go-crypto v1.0.0 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/blendle/zapdriver v1.3.1 // indirect
	github.com/census-instrumentation/opencensus-proto v0.4.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/cloudflare/circl v1.3.7 // indirect
	github.com/danwakefield/fnmatch v0.0.0-20160403171240-cbb64ac3d964 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/emicklei/go-restful/v3 v3.12.1 // indirect
	github.com/emirpasic/gods v1.18.1 // indirect
	github.com/evanphx/json-patch/v5 v5.9.0 // indirect
	github.com/fatih/structs v1.1.0 // indirect
	github.com/fvbommel/sortorder v1.0.1 // indirect
	github.com/go-git/gcfg v1.5.1-0.20230307220236-3a3c6141e376 // indirect
	github.com/go-git/go-billy/v5 v5.5.0 // indirect
	github.com/go-kit/log v0.2.1 // indirect
	github.com/go-logfmt/logfmt v0.6.0 // indirect
	github.com/go-logr/logr v1.4.2 // indirect
	github.com/go-logr/zapr v1.3.0 // indirect
	github.com/go-openapi/jsonpointer v0.21.0 // indirect
	github.com/go-openapi/jsonreference v0.21.0 // indirect
	github.com/go-openapi/swag v0.23.0 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang-jwt/jwt v3.2.1+incompatible // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/google/btree v1.0.1 // indirect
	github.com/google/gnostic-models v0.6.8 // indirect
	github.com/google/go-querystring v1.1.0 // indirect
	github.com/google/pprof v0.0.0-20240727154555-813a5fbdbec8 // indirect
	github.com/google/s2a-go v0.1.8 // indirect
	github.com/google/wire v0.4.0 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.3.2 // indirect
	github.com/googleapis/gax-go v2.0.2+incompatible // indirect
	github.com/googleapis/gax-go/v2 v2.13.0 // indirect
	github.com/gorilla/handlers v1.4.2 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.22.0 // indirect
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-cleanhttp v0.5.2 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/imdario/mergo v0.3.16 // indirect
	github.com/jbenet/go-context v0.0.0-20150711004518-d14ea06fba99 // indirect
	github.com/jmespath/go-jmespath v0.4.0 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/kevinburke/ssh_config v1.2.0 // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/moby/spdystream v0.4.0 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/peterbourgon/diskv v2.0.1+incompatible
	github.com/pjbgf/sha1cd v0.3.0 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/prometheus/procfs v0.15.1 // indirect
	github.com/prometheus/statsd_exporter v0.27.1 // indirect
	github.com/sergi/go-diff v1.3.2-0.20230802210424-5b0b94c5c0d3 // indirect
	github.com/shurcooL/graphql v0.0.0-20181231061246-d48a9a75455f // indirect
	github.com/skeema/knownhosts v1.2.2 // indirect
	github.com/smartystreets/goconvey v1.8.1 // indirect
	github.com/trivago/tgo v1.0.7 // indirect
	github.com/xanzy/ssh-agent v0.3.3 // indirect
	go.opencensus.io v0.24.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/crypto v0.26.0 // indirect
	golang.org/x/exp v0.0.0-20240823005443-9b4947da3948 // indirect
	golang.org/x/mod v0.20.0 // indirect
	golang.org/x/sys v0.24.0 // indirect
	golang.org/x/term v0.23.0 // indirect
	golang.org/x/tools v0.24.0 // indirect
	golang.org/x/xerrors v0.0.0-20231012003039-104605ab7028 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240827150818-7e3bb234dfed // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/warnings.v0 v0.1.2 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	k8s.io/apiextensions-apiserver v0.31.0 // indirect
	k8s.io/klog/v2 v2.130.1 // indirect
	k8s.io/kube-openapi v0.0.0-20240827152857-f7e401e7b4c2 // indirect
	sigs.k8s.io/json v0.0.0-20221116044647-bc3834ca7abd // indirect
	sigs.k8s.io/structured-merge-diff/v4 v4.4.1 // indirect
)

require (
	cloud.google.com/go/auth v0.9.1 // indirect
	cloud.google.com/go/auth/oauth2adapt v0.2.4 // indirect
	dario.cat/mergo v1.0.0 // indirect
	github.com/antlr4-go/antlr/v4 v4.13.1 // indirect
	github.com/cyphar/filepath-securejoin v0.2.4 // indirect
	github.com/felixge/httpsnoop v1.0.4 // indirect
	github.com/fxamacker/cbor/v2 v2.7.0 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/google/cel-go v0.21.0 // indirect
	github.com/gorilla/websocket v1.5.1 // indirect
	github.com/klauspost/compress v1.17.9 // indirect
	github.com/kylelemons/godebug v1.1.0 // indirect
	github.com/mxk/go-flowrate v0.0.0-20140419014527-cca7078d478f // indirect
	github.com/stoewer/go-strcase v1.3.0 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc v0.53.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.53.0 // indirect
	go.opentelemetry.io/otel v1.28.0 // indirect
	go.opentelemetry.io/otel/metric v1.28.0 // indirect
	go.opentelemetry.io/otel/trace v1.28.0 // indirect
	gopkg.in/evanphx/json-patch.v4 v4.12.0 // indirect
)
