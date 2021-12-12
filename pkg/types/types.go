package types

import "github.com/google/go-github/v41/github"

type Config struct {
	Apps    Apps    `yaml:"apps"`
	Logging Logging `yaml:"logging"`
	Repo    Repo    `yaml:"repo"`
	Server  Server  `yaml:"server"`
}

type Apps struct {
	BotName string `yaml:"botName"`
	GitHub  App    `yaml:"github"`
	Client  App    `yaml:"client"`
}

type App struct {
	Org            string `yaml:"org"`
	AppID          int64  `yaml:"appID"`
	InstallationID int64  `yaml:"installationID"`
	PrivateKey     string `yaml:"privateKey"`
}

type Logging struct {
	Compression  bool   `yaml:"compression"`
	Ephemeral    bool   `yaml:"ephemeral"`
	Level        string `yaml:"level"`
	LogDirectory string `yaml:"logDirectory"`
	MaxAge       int    `yaml:"maxAge"`
	MaxBackups   int    `yaml:"maxBackups"`
	MaxSize      int    `yaml:"maxSize"`
}

type Server struct {
	Address   string  `yaml:"address"`
	Port      int     `yaml:"port"`
	RateLimit float64 `yaml:"rateLimit"`
	TLS       TLS     `yaml:"tls"`
}

type TLS struct {
	Enabled  bool   `yaml:"enabled"`
	CertFile string `yaml:"certFile"`
	KeyFile  string `yaml:"keyFile"`
}

type Repo struct {
	Org  string `yaml:"org"`
	Name string `yaml:"name"`
}

type WebHook struct {
	Action     string               `json:"action"`
	Comment    *github.IssueComment `json:"comment"`
	Issue      *github.Issue        `json:"issue"`
	Repository *github.Repository   `json:"repository"`
	Change     *github.EditChange   `json:"change"`
}
