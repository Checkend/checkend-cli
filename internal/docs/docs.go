// Package docs is the one table of the docs links that ce prints with its errors. Each topic is
// "<page>#<anchor>" of https://checkend.com/docs/. The site makes the anchors from the headings
// of the guides: lower case, punctuation removed, spaces as "-", a repeated heading with "-1".
// A heading that changes changes its anchor: run the live link test (CE_DOCS_LIVE=1) after a
// docs change.
package docs

// Base is the root of the public docs.
const Base = "https://checkend.com/docs/"

// Topic is a page and an anchor of the docs, for example "operations#upgrade". "" is no topic.
type Topic string

// The topics. Add a topic here, and only here; the tests check every topic that the code uses.
const (
	QuickStart       Topic = "quick-start"
	SelfHost         Topic = "self-host"
	SelfHostDocker   Topic = "self-host#with-docker"
	SelfHostServer   Topic = "self-host#without-docker"
	SelfHostProxy    Topic = "self-host#behind-a-reverse-proxy"
	DeployCloudflare Topic = "quick-start#2-or-deploy-it-to-your-cloudflare-account"
	Backup           Topic = "operations#back-up"
	Upgrade          Topic = "operations#upgrade"
	CheckInstall     Topic = "operations#check-an-install"
	Operations       Topic = "operations"
	Authentication   Topic = "api#authentication"
	Scopes           Topic = "api#scopes"
	Tokens           Topic = "api#tokens"
	APIErrors        Topic = "api#errors"
	Projects         Topic = "api#projects"
	Issues           Topic = "api#issues"
	Deploys          Topic = "api#releases-and-deploys"
	Settings         Topic = "settings#settings"
	PublicURL        Topic = "settings#public_url"
	Health           Topic = "settings#health"
	BackPressure     Topic = "sending-errors#back-pressure-429"
	RequestLimits    Topic = "sending-errors#limits-on-requests"
	SendingErrors    Topic = "sending-errors"
	AgentSetup       Topic = "agent-setup"
	SetupKey         Topic = "agent-setup#1-make-a-setup-key"
	ConnectAgent     Topic = "agent-setup#connect-the-agent"
	MCP              Topic = "mcp#connect"
	SourceMaps       Topic = "source-maps#upload-the-source-maps"
	SourceMapStorage Topic = "source-maps#set-up-storage"
)

// All are all the topics, for the tests.
var All = []Topic{
	QuickStart, SelfHost, SelfHostDocker, SelfHostServer, SelfHostProxy, DeployCloudflare,
	Backup, Upgrade, CheckInstall, Operations, Authentication, Scopes, Tokens, APIErrors, Projects, Issues,
	Deploys, Settings, PublicURL, Health, BackPressure, RequestLimits, SendingErrors, AgentSetup,
	SetupKey, ConnectAgent, MCP, SourceMaps, SourceMapStorage,
}

// URL is the full link of a topic.
func URL(t Topic) string { return Base + string(t) }
