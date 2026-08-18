module github.com/DopplerHQ/cli

// GO_VERSION_DEF
go 1.26.2

require (
	github.com/AlecAivazis/survey/v2 v2.3.6
	github.com/DopplerHQ/agent-proxy v0.0.0-00010101000000-000000000000
	github.com/DopplerHQ/gocui v0.1.0
	github.com/atotto/clipboard v0.1.4
	github.com/google/uuid v1.6.0
	github.com/hashicorp/go-version v1.9.0
	github.com/jedib0t/go-pretty v4.3.0+incompatible
	github.com/jesseduffield/lazycore v0.0.0-20221012050358-03d2e40243c5
	github.com/mattn/go-isatty v0.0.16
	github.com/sasha-s/go-deadlock v0.3.1
	github.com/sirupsen/logrus v1.9.3
	github.com/skratchdot/open-golang v0.0.0-20200116055534-eef842397966
	github.com/spf13/cobra v1.7.0
	github.com/stretchr/testify v1.11.1
	github.com/zalando/go-keyring v0.2.8
	golang.org/x/crypto v0.55.0
	golang.org/x/exp v0.0.0-20260718201538-764159d718ef
	golang.org/x/sync v0.22.0
	gopkg.in/gookit/color.v1 v1.1.6
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/asaskevich/govalidator v0.0.0-20210307081110-f21760c49a8d // indirect
	github.com/cncf/xds/go v0.0.0-20260202195803-dba9d589def2 // indirect
	github.com/danieljoos/wincred v1.2.3 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/ebitengine/purego v0.10.0 // indirect
	github.com/envoyproxy/go-control-plane/envoy v1.39.0 // indirect
	github.com/envoyproxy/protoc-gen-validate v1.3.3 // indirect
	github.com/gdamore/encoding v1.0.0 // indirect
	github.com/gdamore/tcell/v2 v2.4.0 // indirect
	github.com/go-errors/errors v1.0.2 // indirect
	github.com/go-ole/go-ole v1.2.6 // indirect
	github.com/go-openapi/errors v0.20.3 // indirect
	github.com/go-openapi/strfmt v0.21.3 // indirect
	github.com/godbus/dbus/v5 v5.2.2 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/kballard/go-shellquote v0.0.0-20180428030007-95032a82bc51 // indirect
	github.com/lucasb-eyer/go-colorful v1.0.3 // indirect
	github.com/lufia/plan9stats v0.0.0-20220913051719-115f729f3c8c // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-runewidth v0.0.14 // indirect
	github.com/mgutz/ansi v0.0.0-20200706080929-d51e80ef957d // indirect
	github.com/mitchellh/mapstructure v1.5.0 // indirect
	github.com/oklog/ulid v1.3.1 // indirect
	github.com/petermattis/goid v0.0.0-20180202154549-b0b1615b78e5 // indirect
	github.com/planetscale/vtprotobuf v0.6.1-0.20240319094008-0393e58bdf10 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/power-devops/perfstat v0.0.0-20240221224432-82ca36839d55 // indirect
	github.com/rivo/uniseg v0.4.2 // indirect
	github.com/samber/lo v1.31.0 // indirect
	github.com/shirou/gopsutil/v4 v4.26.3 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	github.com/tetratelabs/func-e v1.6.0 // indirect
	github.com/tklauser/go-sysconf v0.3.16 // indirect
	github.com/tklauser/numcpus v0.11.0 // indirect
	github.com/ulikunitz/lz v0.6.11 // indirect
	github.com/ulikunitz/xz/v2 v2.0.0-dev.4 // indirect
	github.com/yusufpapurcu/wmi v1.2.4 // indirect
	go.mongodb.org/mongo-driver v1.17.9 // indirect
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/term v0.45.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260526163538-3dc84a4a5aaa // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260526163538-3dc84a4a5aaa // indirect
	google.golang.org/grpc v1.83.2 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

replace github.com/DopplerHQ/agent-proxy => ../agent-proxy
