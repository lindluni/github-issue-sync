// TODO: Decide which edits should be allowed, and which should be reverted.
// TODO: If we delete issue, recreate it (this is complicated, as it requires us to remap the issue/comment numbers and id's )
// TODO: If issue/comment is deleted, and we cannot find it, comment back that the other person has deleted the item
//       and please open a new issue.
// TODO: Add loggers/clients to handlers
package main

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/gin-contrib/requestid"
	"github.com/gin-gonic/gin"
	"github.com/google/go-github/v41/github"
	"github.com/google/uuid"
	"github.com/lindluni/github-issue-sync/pkg/db"
	"github.com/lindluni/github-issue-sync/pkg/handlers"
	"github.com/lindluni/github-issue-sync/pkg/server"
	"github.com/lindluni/github-issue-sync/pkg/types"
	"github.com/shurcooL/githubv4"
	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/writer"
	"gopkg.in/natefinch/lumberjack.v2"
	"gopkg.in/yaml.v2"
)

func main() {
	config, githubPrivateKey, clientPrivateKey := initConfig()
	logger := initLogger(config)

	logger.Debug("Creating GitHub application transports")
	itrForClient, err := ghinstallation.NewAppsTransport(http.DefaultTransport, config.Apps.Client.AppID, clientPrivateKey)
	if err != nil {
		logger.Fatalf("Failed creating app authentication: %v", err)
	}
	itrForGitHub, err := ghinstallation.New(http.DefaultTransport, config.Apps.GitHub.AppID, config.Apps.GitHub.InstallationID, githubPrivateKey)
	if err != nil {
		logger.Fatalf("Failed creating installation authentication: %v", err)
	}
	logger.Debug("Created GitHub application installation configuration")

	logger.Debug("Creating client")
	client := github.NewClient(&http.Client{Transport: itrForClient})
	logger.Debug("Created client")

	logger.Debug("Creating GitHub client")
	gitHubClient := github.NewClient(&http.Client{Transport: itrForGitHub})
	logger.Debug("Created GitHub client")

	logger.Debug("Creating GitHub GraphQL client")
	graphQLClient := githubv4.NewClient(&http.Client{Transport: itrForGitHub})
	logger.Debug("Created GitHub GraphQL client")

	logger.Info("Initialize Router")
	router := gin.New()
	router.Use(requestid.New(requestid.Config{
		Generator: func() string {
			return uuid.NewString()
		},
	}))
	router.Use(gin.Logger())
	logger.Debug("Initialized Router")

	dbClient, err := sql.Open("mysql", "root:root@/")
	if err != nil {
		panic(err)
	}
	dbClient.SetConnMaxLifetime(time.Minute * 3)
	dbClient.SetMaxOpenConns(10)
	dbClient.SetMaxIdleConns(10)

	dbManager := &db.Manager{
		Client: dbClient,
	}

	manager := &server.Manager{
		Logger: logger,
		Config: config,
		Router: router,
		Server: &http.Server{
			Addr:    net.JoinHostPort(config.Server.Address, strconv.Itoa(config.Server.Port)),
			Handler: router,
		},
		Client:        client,
		DBClient:      dbManager,
		GitHubClient:  gitHubClient,
		GraphQLClient: graphQLClient,
		EMUHandler: &handlers.EMU{
			Client:        client,
			DBClient:      dbManager,
			GitHubClient:  gitHubClient,
			GraphQLClient: graphQLClient,
			Config:        config,
			Logger:        logger,
		},
		GitHubHandler: &handlers.GitHub{
			Client:        client,
			DBClient:      dbManager,
			GitHubClient:  gitHubClient,
			GraphQLClient: graphQLClient,
			Config:        config,
			Logger:        logger,
		},
	}

	err = manager.DBClient.InitDB()
	if err != nil {
		panic(err)
	}
	manager.Serve()
}

func initConfig() (*types.Config, []byte, []byte) {
	var bytes []byte
	var err error
	logrus.Info("Loading configuration")
	configPath, set := os.LookupEnv("CONFIG_PATH")
	if set {
		bytes, err = ioutil.ReadFile(configPath)
	} else {
		bytes, err = ioutil.ReadFile("config.yml")
	}
	if err != nil {
		logrus.Fatalf("Unable to parse config file: %v", err)
	}
	logrus.Info("Configuration loaded")

	logrus.Info("Parsing configuration")
	config := &types.Config{}
	err = yaml.Unmarshal(bytes, &config)
	if err != nil {
		logrus.Fatalf("Unable to parse config file: %v", err)
	}
	logrus.Info("Configuration parsed")

	logrus.Info("Validating configuration")
	if !config.Logging.Ephemeral {
		if config.Logging.LogDirectory == "" || config.Logging.MaxSize <= 0 || config.Logging.MaxAge <= 0 {
			logrus.Fatal("Logging in non-ephemeral mode requires you set the following logging values: logDirectory, maxAge, maxSize")
		}
	}

	if config.Logging.Level == "" {
		config.Logging.Level = "info"
	}
	logrus.Info("Configuration validated")

	logrus.Info("Decoding GitHub private key")
	githubPrivateKey, err := base64.StdEncoding.DecodeString(config.Apps.GitHub.PrivateKey)
	if err != nil {
		logrus.Fatalf("Unable to decode private key from base64: %v", err)
	}
	logrus.Info("GitHub Private key decoded")

	logrus.Info("Decoding Client private key")

	clientPrivateKey, err := base64.StdEncoding.DecodeString(config.Apps.Client.PrivateKey)
	if err != nil {
		logrus.Fatalf("Unable to decode private key from base64: %v", err)
	}
	logrus.Info("GitHub Client key decoded")

	return config, githubPrivateKey, clientPrivateKey
}

func initLogger(config *types.Config) *logrus.Logger {
	gin.SetMode(gin.ReleaseMode)
	logger := logrus.New()
	level, err := logrus.ParseLevel(config.Logging.Level)
	if err != nil {
		logrus.Fatalf("Unable to parse logging level: %v", err)
	}
	logger.SetLevel(level)

	logger.Debug("Marshalling logging configuration")
	bytes, err := json.Marshal(config.Logging)
	if err != nil {
		logger.Fatalf("Unable to marshal logging configuration: %v", err)
	}
	logger.Debug("Marshalled logging configuration")

	logger.Debugf("Initializing logger with configuration: %s", string(bytes))
	if !config.Logging.Ephemeral {
		logPath := filepath.Join(config.Logging.LogDirectory, "/actions-runner-manager/server.log")
		rotator := &lumberjack.Logger{
			Compress:   config.Logging.Compression,
			Filename:   logPath,
			MaxBackups: config.Logging.MaxBackups,
			MaxAge:     config.Logging.MaxAge,
			MaxSize:    config.Logging.MaxSize,
		}
		logger.SetOutput(ioutil.Discard)
		logger.SetFormatter(&logrus.TextFormatter{
			FullTimestamp: true,
		})
		logger.AddHook(&writer.Hook{ // Send logs with level higher than warning to stderr
			Writer: io.MultiWriter(os.Stderr, rotator),
			LogLevels: []logrus.Level{
				logrus.PanicLevel,
				logrus.FatalLevel,
				logrus.ErrorLevel,
				logrus.WarnLevel,
			},
		})
		logger.AddHook(&writer.Hook{ // Send info and debug logs to stdout
			Writer: io.MultiWriter(os.Stdout, rotator),
			LogLevels: []logrus.Level{
				logrus.InfoLevel,
				logrus.DebugLevel,
			},
		})

	}
	logger.Debug("Logger initialized")
	return logger
}
