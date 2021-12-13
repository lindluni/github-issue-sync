package server

// TODO: Verify EMU repo is a known repo from config updated via Actions rollout
// TODO: Return response objects on all paths

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/gin-gonic/gin"
	"github.com/google/go-github/v41/github"
	"github.com/lindluni/github-issue-sync/pkg/db"
	"github.com/lindluni/github-issue-sync/pkg/handlers"
	"github.com/lindluni/github-issue-sync/pkg/types"
	"github.com/shurcooL/githubv4"
	"github.com/sirupsen/logrus"
	//"github.com/gin-contrib/requestid"
)

type Manager struct {
	Client        *github.Client
	DBClient      *db.Manager
	GitHubClient  *github.Client
	GraphQLClient *githubv4.Client

	Router *gin.Engine
	Server *http.Server

	Config *types.Config
	Logger *logrus.Logger
}

func (m *Manager) Serve() {
	m.Logger.Info("Initializing API endpoints")
	m.SetRoutes()

	m.Logger.Info("Configuring OS signal handling")
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc,
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGQUIT)
	go func() {
		<-sigc
		err := m.Server.Shutdown(context.Background())
		m.Logger.Errorf("Failed to shutdown server: %v", err)
	}()
	m.Logger.Debug("Configured OS signal handling")

	m.Logger.Debug("Compiling HTTP server address")
	address := fmt.Sprintf("%s:%d", "", m.Config.Server.Port)
	m.Logger.Infof("Starting API server on address: %s", address)
	if m.Config.Server.TLS.Enabled {
		err := m.Server.ListenAndServeTLS(m.Config.Server.TLS.CertFile, m.Config.Server.TLS.KeyFile)
		if err != nil {
			m.Logger.Fatalf("API server failed: %v", err)
		}
	} else {
		err := m.Server.ListenAndServe()
		if err != nil {
			m.Logger.Fatalf("API server failed: %v", err)
		}
	}
}

func (m *Manager) SetRoutes() {
	v1 := m.Router.Group("/webhooks")
	{
		// Events triggered by GitHub Professional Services
		v1.POST("/github", m.DoWebHookGitHub)

		// Events triggered by EMU
		v1.POST("/emu", m.DoWebHookEMU)
	}
	m.Logger.Debug("Initialized routes")
}

func (m *Manager) DoWebHookEMU(c *gin.Context) {
	event := c.GetHeader("X-GitHub-Event")
	emu := handlers.EMU{}
	switch event {
	case "issues":
		webhook, err := parseWebHook(c)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if !m.isBotIssue(webhook) {
			err = emu.HandleIssue(webhook, m.DBClient, m.GitHubClient, m.GraphQLClient, m.Config)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
		}
	case "issue_comment":
		webhook, err := parseWebHook(c)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if !m.isBotComment(webhook) {
			err = emu.HandleIssueComment(webhook, m.DBClient, m.GitHubClient, m.Config)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
		}
	default:
		fmt.Printf("Unsupported event: %s\n", event)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Unsupported event"})
		return
	}
}

func (m *Manager) DoWebHookGitHub(c *gin.Context) {
	event := c.GetHeader("X-GitHub-Event")
	gh := handlers.GitHub{}
	switch event {
	case "issues":
		webhook, err := parseWebHook(c)
		if !m.isBotIssue(webhook) || (webhook.Action == "edited" && !m.isBotSender(webhook)) {
			err = gh.HandleIssue(webhook, m.Client, m.DBClient, m.GitHubClient, m.Config)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
		}
	case "issue_comment":
		webhook, err := parseWebHook(c)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if !m.isBotComment(webhook) {
			err = gh.HandleIssueComment(webhook, m.Client, m.DBClient, m.Config)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
		}
	default:
		fmt.Printf("Unsupported event: %s\n", event)
		c.JSON(http.StatusOK, gin.H{"Error": "Unsupported event"})
	}
}

func parseWebHook(c *gin.Context) (*types.WebHook, error) {
	var webhook *types.WebHook
	err := c.BindJSON(&webhook)
	if err != nil {
		return nil, err
	}
	return webhook, nil
}

func (m *Manager) isBotComment(webhook *types.WebHook) bool {
	return webhook.Comment.User.GetLogin() == m.Config.Apps.EMUBotName || webhook.Comment.User.GetLogin() == m.Config.Apps.ClientBotName
}

func (m *Manager) isBotIssue(webhook *types.WebHook) bool {
	return webhook.Issue.User.GetLogin() == m.Config.Apps.EMUBotName || webhook.Issue.User.GetLogin() == m.Config.Apps.ClientBotName
}

func (m *Manager) isBotSender(webhook *types.WebHook) bool {
	return webhook.Sender.GetLogin() == m.Config.Apps.EMUBotName || webhook.Sender.GetLogin() == m.Config.Apps.ClientBotName
}
