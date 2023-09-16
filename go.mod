module github.com/borderzero/border0-cli

go 1.20

require (
	cloud.google.com/go/cloudsqlconn v1.2.4
	github.com/ActiveState/termtest/conpty v0.5.0
	github.com/AlecAivazis/survey/v2 v2.3.6
	github.com/Code-Hex/go-generics-cache v1.3.1
	github.com/TylerBrock/colorjson v0.0.0-20200706003622-8a50f05110d2
	github.com/aws/aws-sdk-go v1.44.271
	github.com/aws/aws-sdk-go-v2 v1.20.0
	github.com/aws/aws-sdk-go-v2/config v1.18.25
	github.com/aws/aws-sdk-go-v2/credentials v1.13.24
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.13.5
	github.com/aws/aws-sdk-go-v2/feature/rds/auth v1.2.11
	github.com/aws/aws-sdk-go-v2/service/cloudformation v1.30.0
	github.com/aws/aws-sdk-go-v2/service/ec2 v1.107.0
	github.com/aws/aws-sdk-go-v2/service/ecs v1.28.1
	github.com/aws/aws-sdk-go-v2/service/secretsmanager v1.19.8
	github.com/aws/aws-sdk-go-v2/service/ssm v1.36.8
	github.com/aws/session-manager-plugin v0.0.0-20230315220744-7b544e9f381d
	github.com/bluele/factory-go v0.0.1
	github.com/borderzero/border0-go v1.3.13
	github.com/borderzero/border0-proto v1.0.3
	github.com/borderzero/discovery v0.1.23
	github.com/brianvoe/gofakeit v3.18.0+incompatible
	github.com/cenkalti/backoff/v4 v4.2.1
	github.com/creack/pty v1.1.18
	github.com/docker/docker v24.0.5+incompatible
	github.com/fatih/color v1.15.0
	github.com/gliderlabs/ssh v0.3.5
	github.com/go-mysql-org/go-mysql v1.7.0
	github.com/golang-jwt/jwt v3.2.2+incompatible
	github.com/google/uuid v1.3.0
	github.com/hashicorp/yamux v0.1.1
	github.com/ivanpirog/coloredcobra v1.0.1
	github.com/jackc/pgconn v1.14.0
	github.com/jackc/pgproto3/v2 v2.3.2
	github.com/jbenet/go-os-rename v0.0.0-20150428075126-3ac97f61ef67
	github.com/jedib0t/go-pretty v4.3.0+incompatible
	github.com/kardianos/service v1.2.2
	github.com/manifoldco/promptui v0.9.0
	github.com/mitchellh/mapstructure v1.5.0
	github.com/moby/term v0.5.0
	github.com/opencontainers/selinux v1.11.0
	github.com/pavlo-v-chernykh/keystore-go/v4 v4.4.1
	github.com/pkg/sftp v1.13.1
	github.com/satori/go.uuid v1.2.0
	github.com/shirou/gopsutil/v3 v3.23.4
	github.com/skratchdot/open-golang v0.0.0-20200116055534-eef842397966
	github.com/songgao/water v0.0.0-20200317203138-2b4b6d7c09d8
	github.com/spf13/cobra v1.7.0
	github.com/spf13/viper v1.15.0
	github.com/stretchr/testify v1.8.4
	go.uber.org/zap v1.24.0
	golang.org/x/crypto v0.12.0
	golang.org/x/net v0.12.0
	golang.org/x/sync v0.3.0
	golang.org/x/sys v0.11.0
	golang.org/x/term v0.11.0
	google.golang.org/grpc v1.56.2
	k8s.io/api v0.27.3
	k8s.io/apimachinery v0.27.3
	k8s.io/client-go v0.27.3
	k8s.io/kubectl v0.27.2
	k8s.io/utils v0.0.0-20230505201702-9f6742963106
	nhooyr.io/websocket v1.8.7
)

require (
	cloud.google.com/go/compute v1.19.3 // indirect
	cloud.google.com/go/compute/metadata v0.2.3 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.1.37 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.4.31 // indirect
	github.com/aws/aws-sdk-go-v2/internal/ini v1.3.34 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.9.31 // indirect
	github.com/aws/aws-sdk-go-v2/service/rds v1.48.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.12.10 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.14.10 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.21.0 // indirect
	github.com/aws/smithy-go v1.14.0 // indirect
	github.com/chzyer/readline v1.5.1 // indirect
	github.com/cihub/seelog v0.0.0-20170130134532-f561c5e57575 // indirect
	github.com/go-ole/go-ole v1.2.6 // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/google/go-cmp v0.5.9 // indirect
	github.com/google/s2a-go v0.1.4 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.2.3 // indirect
	github.com/googleapis/gax-go/v2 v2.9.1 // indirect
	github.com/gorilla/websocket v1.5.0 // indirect
	github.com/imdario/mergo v0.3.6 // indirect
	github.com/jackc/chunkreader/v2 v2.0.1 // indirect
	github.com/jackc/pgio v1.0.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20221227161230-091c0ba34f0a // indirect
	github.com/klauspost/compress v1.13.6 // indirect
	github.com/kr/fs v0.1.0 // indirect
	github.com/lufia/plan9stats v0.0.0-20230326075908-cb1d2100619a // indirect
	github.com/pelletier/go-toml/v2 v2.0.8 // indirect
	github.com/pingcap/errors v0.11.5-0.20210425183316-da1aaba5fb63 // indirect
	github.com/power-devops/perfstat v0.0.0-20221212215047-62379fc7944b // indirect
	github.com/shoenig/go-m1cpu v0.1.6 // indirect
	github.com/shopspring/decimal v1.3.1 // indirect
	github.com/siddontang/go v0.0.0-20180604090527-bdc77568d726 // indirect
	github.com/siddontang/go-log v0.0.0-20190221022429-1e957dd83bed // indirect
	github.com/tklauser/go-sysconf v0.3.11 // indirect
	github.com/tklauser/numcpus v0.6.0 // indirect
	github.com/twinj/uuid v0.0.0-20151029044442-89173bcdda19 // indirect
	github.com/yusufpapurcu/wmi v1.2.3 // indirect
	go.opencensus.io v0.24.0 // indirect
	golang.org/x/exp v0.0.0-20220328175248-053ad81199eb // indirect
	golang.org/x/mod v0.10.0 // indirect
	golang.org/x/tools v0.9.1 // indirect
	google.golang.org/api v0.124.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20230525234020-1aefcd67740a // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20230710151506-e685fd7b542b // indirect
)

require (
	github.com/Azure/go-ansiterm v0.0.0-20230124172434-306776ec8161 // indirect
	github.com/Microsoft/go-winio v0.6.1 // indirect
	github.com/anmitsu/go-shlex v0.0.0-20200514113438-38f4b401e2be // indirect
	github.com/asaskevich/govalidator v0.0.0-20230301143203-a9d515a09cc2 // indirect
	github.com/aws/aws-sdk-go-v2/service/ec2instanceconnect v1.15.12
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/docker/distribution v2.8.2+incompatible // indirect
	github.com/docker/go-connections v0.4.0 // indirect
	github.com/docker/go-units v0.5.0 // indirect
	github.com/emicklei/go-restful/v3 v3.10.2 // indirect
	github.com/fsnotify/fsnotify v1.6.0 // indirect
	github.com/go-logr/logr v1.2.4 // indirect
	github.com/go-openapi/errors v0.20.3 // indirect
	github.com/go-openapi/jsonpointer v0.19.6 // indirect
	github.com/go-openapi/jsonreference v0.20.2 // indirect
	github.com/go-openapi/strfmt v0.21.7 // indirect
	github.com/go-openapi/swag v0.22.3 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang/protobuf v1.5.3 // indirect
	github.com/google/gnostic v0.6.9 // indirect
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/hashicorp/hcl v1.0.0 // indirect
	github.com/hokaccha/go-prettyjson v0.0.0-20211117102719-0474bc63780f // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/jmespath/go-jmespath v0.4.0 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/kballard/go-shellquote v0.0.0-20180428030007-95032a82bc51 // indirect
	github.com/magiconair/properties v1.8.7 // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.19 // indirect
	github.com/mattn/go-runewidth v0.0.14 // indirect
	github.com/mgutz/ansi v0.0.0-20200706080929-d51e80ef957d // indirect
	github.com/mitchellh/go-wordwrap v1.0.1 // indirect
	github.com/moby/spdystream v0.2.0 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/oklog/ulid v1.3.1 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/opencontainers/image-spec v1.0.2 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/prometheus/procfs v0.9.0
	github.com/rivo/uniseg v0.4.4 // indirect
	github.com/spf13/afero v1.9.5 // indirect
	github.com/spf13/cast v1.5.1 // indirect
	github.com/spf13/jwalterweatherman v1.1.0 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	github.com/stretchr/objx v0.5.0 // indirect
	github.com/subosito/gotenv v1.4.2 // indirect
	go.mongodb.org/mongo-driver v1.11.6 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/oauth2 v0.8.0 // indirect
	golang.org/x/text v0.12.0 // indirect
	golang.org/x/time v0.3.0 // indirect
	google.golang.org/appengine v1.6.7 // indirect
	google.golang.org/protobuf v1.31.0
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/ini.v1 v1.67.0 // indirect
	gopkg.in/yaml.v2 v2.4.0
	gopkg.in/yaml.v3 v3.0.1
	k8s.io/klog/v2 v2.100.1 // indirect
	k8s.io/kube-openapi v0.0.0-20230525220651-2546d827e515 // indirect
	sigs.k8s.io/json v0.0.0-20221116044647-bc3834ca7abd // indirect
	sigs.k8s.io/structured-merge-diff/v4 v4.2.3 // indirect
	sigs.k8s.io/yaml v1.3.0 // indirect
)
