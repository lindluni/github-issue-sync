package server

// TODO: Verify EMU repo is a known repo from config updated via Actions rollout
// TODO: Return response objects on all paths

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/gin-gonic/gin"
	"github.com/google/go-github/v41/github"
	"github.com/lindluni/github-issue-sync/pkg/db"
	"github.com/lindluni/github-issue-sync/pkg/types"
	"github.com/shurcooL/githubv4"
	"github.com/sirupsen/logrus"
	//"github.com/gin-contrib/requestid"
)

var installationClients = make(map[string]*github.Client)

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
	switch event {
	case "issues":
		webhook, err := parseWebHook(c)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if !m.isBotIssue(webhook) {
			err = m.handleEMUIssue(webhook)
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
			err = m.handleEMUIssueComment(webhook)
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
	switch event {
	case "issues":
		webhook, err := parseWebHook(c)
		if !m.isBotIssue(webhook) {
			err = m.handleGitHubIssue(webhook)
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
			err = m.handleGitHubIssueComment(webhook)
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

func (m *Manager) isBotComment(webhook *types.WebHook) bool {
	return webhook.Comment.User.GetLogin() != m.Config.Apps.EMUBotName && webhook.Comment.User.GetLogin() != m.Config.Apps.ClientBotName
}

func (m *Manager) isBotIssue(webhook *types.WebHook) bool {
	return webhook.Issue.User.GetLogin() != m.Config.Apps.EMUBotName && webhook.Issue.User.GetLogin() != m.Config.Apps.ClientBotName
}

func parseWebHook(c *gin.Context) (*types.WebHook, error) {
	var webhook *types.WebHook
	err := c.BindJSON(&webhook)
	if err != nil {
		return nil, err
	}
	return webhook, nil
}

func (m *Manager) handleEMUIssue(webhook *types.WebHook) error {
	switch webhook.Action {
	case "opened":
		issue, err := m.openEMUIssue(webhook)
		if err != nil {
			return err
		}
		err = m.DBClient.InsertIssueEntry(webhook, issue.GetNumber())
		if err != nil {
			return err
		}
	case "edited":
		err := m.editEMUIssue(webhook)
		if err != nil {
			return err
		}
		err = m.DBClient.UpdateIssueEntry(webhook)
		if err != nil {
			return err
		}
	case "deleted":
		err := m.deleteEMUIssue(webhook)
		if err != nil {
			return err
		}
		err = m.DBClient.DeleteIssueEntry(webhook)
		if err != nil {
			return err
		}
	case "closed":
		err := m.updateEMUIssueState(webhook)
		if err != nil {
			return err
		}
		err = m.DBClient.UpdateIssueEntry(webhook)
		if err != nil {
			return err
		}
	case "reopened":
		err := m.updateEMUIssueState(webhook)
		if err != nil {
			return err
		}
		err = m.DBClient.UpdateIssueEntry(webhook)
		if err != nil {
			return err
		}

	}
	return nil
}

func (m *Manager) openEMUIssue(webhook *types.WebHook) (*github.Issue, error) {
	org := webhook.Repository.Owner.GetLogin()
	repo := webhook.Repository.GetName()
	issueNumber := webhook.Issue.GetNumber()
	title := webhook.Issue.GetTitle()
	author := webhook.Issue.User.GetLogin()
	body := webhook.Issue.GetBody()

	newTitle := fmt.Sprintf("%s/%s#%d: %s", org, repo, issueNumber, title)
	newBody := fmt.Sprintf("@%s posted:\n\n%s", author, body)

	issue, _, err := m.GitHubClient.Issues.Create(context.Background(), m.Config.Repo.Org, m.Config.Repo.Name, &github.IssueRequest{
		Title: &newTitle,
		Body:  &newBody,
	})

	return issue, err
}

func (m *Manager) editEMUIssue(webhook *types.WebHook) error {
	org := webhook.Repository.Owner.GetLogin()
	repo := webhook.Repository.GetName()
	issueNumber := webhook.Issue.GetNumber()
	title := webhook.Issue.GetTitle()
	author := webhook.Issue.User.GetLogin()
	body := webhook.Issue.GetBody()

	githubIssueNumber, err := m.DBClient.GetGitHubIssueIDEntry(webhook)
	if err != nil {
		return err
	}

	newTitle := fmt.Sprintf("%s/%s#%d: %s", org, repo, issueNumber, title)
	newBody := fmt.Sprintf("@%s posted:\n\n%s", author, body)

	_, _, err = m.GitHubClient.Issues.Edit(context.Background(), m.Config.Repo.Org, m.Config.Repo.Name, githubIssueNumber, &github.IssueRequest{
		Title: &newTitle,
		Body:  &newBody,
	})

	return err
}

func (m *Manager) deleteEMUIssue(webhook *types.WebHook) error {
	githubIssueNumber, err := m.DBClient.GetGitHubIssueIDEntry(webhook)
	if err != nil {
		return err
	}

	var mutation struct {
		DeleteIssue struct {
			Repository struct {
				ID string
			}
		} `graphql:"deleteIssue(input: $input)"`
	}

	issue, _, err := m.GitHubClient.Issues.Get(context.Background(), m.Config.Repo.Org, m.Config.Repo.Name, githubIssueNumber)
	if err != nil {
		return err
	}
	input := githubv4.DeleteIssueInput{
		IssueID: issue.GetNodeID(),
	}
	err = m.GraphQLClient.Mutate(context.Background(), &mutation, input, nil)
	if err != nil {
		return err
	}

	return nil
}

func (m *Manager) updateEMUIssueState(webhook *types.WebHook) error {
	githubIssueNumber, err := m.DBClient.GetGitHubIssueIDEntry(webhook)
	if err != nil {
		return err
	}
	_, _, err = m.GitHubClient.Issues.Edit(context.Background(), m.Config.Repo.Org, m.Config.Repo.Name, githubIssueNumber, &github.IssueRequest{
		State: webhook.Issue.State,
	})

	return err
}

func (m *Manager) handleEMUIssueComment(webhook *types.WebHook) error {
	switch webhook.Action {
	case "created":
		id, err := m.createEMUComment(webhook)
		if err != nil {
			return err
		}
		err = m.DBClient.InsertCommentEntry(webhook, id)
		if err != nil {
			return err
		}
	case "edited":
		err := m.editEMUComment(webhook)
		if err != nil {
			return err
		}
		err = m.DBClient.UpdateCommentEntry(webhook)
		if err != nil {
			return err
		}
	case "deleted":
		err := m.deleteEMUComment(webhook)
		if err != nil {
			return err
		}
		err = m.DBClient.DeleteCommentEntry(webhook)
		if err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) createEMUComment(webhook *types.WebHook) (int64, error) {
	githubIssueNumber, err := m.DBClient.GetGitHubIssueIDEntry(webhook)
	if err != nil {
		return -1, err
	}
	author := webhook.Comment.User.GetLogin()
	body := webhook.Comment.GetBody()

	newBody := fmt.Sprintf("@%s posted:\n\n%s", author, body)

	comment, _, err := m.GitHubClient.Issues.CreateComment(context.Background(), m.Config.Repo.Org, m.Config.Repo.Name, githubIssueNumber, &github.IssueComment{
		Body: &newBody,
	})
	if err != nil {
		return -1, err
	}
	return comment.GetID(), nil
}

func (m *Manager) editEMUComment(webhook *types.WebHook) error {
	githubIssueNumber, err := m.DBClient.GetGitHubCommentIDEntry(webhook)
	if err != nil {
		return err
	}
	author := webhook.Comment.User.GetLogin()
	body := webhook.Comment.GetBody()

	newBody := fmt.Sprintf("@%s posted:\n\n%s", author, body)
	_, _, err = m.GitHubClient.Issues.EditComment(context.Background(), m.Config.Repo.Org, m.Config.Repo.Name, int64(githubIssueNumber), &github.IssueComment{
		Body: &newBody,
	})
	if err != nil {
		return err
	}
	return nil
}

func (m *Manager) deleteEMUComment(webhook *types.WebHook) error {
	githubIssueNumber, err := m.DBClient.GetGitHubCommentIDEntry(webhook)
	if err != nil {
		return err
	}

	_, err = m.GitHubClient.Issues.DeleteComment(context.Background(), m.Config.Repo.Org, m.Config.Repo.Name, int64(githubIssueNumber))
	if err != nil {
		return err
	}
	return nil
}

func (m *Manager) handleGitHubIssue(webhook *types.WebHook) error {
	switch webhook.Action {
	case "edited":
		err := m.editGitHubIssue(webhook)
		if err != nil {
			return err
		}
		err = m.DBClient.UpdateIssueEntry(webhook)
		if err != nil {
			return err
		}
	case "closed":
		err := m.updateGitHubIssueState(webhook)
		if err != nil {
			return err
		}
		err = m.DBClient.UpdateIssueEntry(webhook)
		if err != nil {
			return err
		}
	case "reopened":
		err := m.updateGitHubIssueState(webhook)
		if err != nil {
			return err
		}
		err = m.DBClient.UpdateIssueEntry(webhook)
		if err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) editGitHubIssue(webhook *types.WebHook) error {
	// TODO: Revert any changes made here
	fmt.Println("editing issues is not allowed")
	return nil
}

func (m *Manager) updateGitHubIssueState(webhook *types.WebHook) error {
	org, repo, issueNumber, err := m.DBClient.GetEMUIssue(webhook)
	if err != nil {
		return err
	}
	client, err := m.retrieveInstallationClient(org)
	if err != nil {
		return err
	}
	_, _, err = client.Issues.Edit(context.Background(), org, repo, issueNumber, &github.IssueRequest{
		State: webhook.Issue.State,
	})
	return err
}

func (m *Manager) handleGitHubIssueComment(webhook *types.WebHook) error {
	switch webhook.Action {
	case "created":
		emuIssueID, emuCommentID, err := m.createGitHubComment(webhook)
		if err != nil {
			return err
		}
		err = m.DBClient.InsertGitHubCommentEntry(webhook, emuIssueID, emuCommentID)
		if err != nil {
			return err
		}
	case "edited":
		err := m.editGitHubComment(webhook)
		if err != nil {
			return err
		}
		err = m.DBClient.UpdateCommentEntry(webhook)
		if err != nil {
			return err
		}
	case "deleted":
		err := m.deleteGitHubComment(webhook)
		if err != nil {
			return err
		}
		err = m.DBClient.DeleteCommentEntry(webhook)
		if err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) createGitHubComment(webhook *types.WebHook) (int64, int64, error) {
	emuIssueID, emuOrg, emuRepo, emuIssueNumber, err := m.DBClient.GetEMUIssueIDFromGitHubCommentEntry(webhook)
	if err != nil {
		return -1, -1, err
	}
	author := webhook.Comment.User.GetLogin()
	body := webhook.Comment.GetBody()

	newBody := fmt.Sprintf("@%s posted:\n\n%s", author, body)
	client, err := m.retrieveInstallationClient(emuOrg)
	if err != nil {
		return -1, -1, err
	}
	comment, _, err := client.Issues.CreateComment(context.Background(), emuOrg, emuRepo, emuIssueNumber, &github.IssueComment{
		Body: &newBody,
	})
	if err != nil {
		return -1, -1, err
	}
	return emuIssueID, comment.GetID(), nil
}

func (m *Manager) editGitHubComment(webhook *types.WebHook) error {
	emuOrg, emuRepo, emuIssueNumber, err := m.DBClient.GetEMUCommentIDEntry(webhook)
	if err != nil {
		return err
	}
	author := webhook.Comment.User.GetLogin()
	body := webhook.Comment.GetBody()
	newBody := fmt.Sprintf("@%s posted:\n\n%s", author, body)

	client, err := m.retrieveInstallationClient(emuOrg)
	if err != nil {
		return err
	}
	_, _, err = client.Issues.EditComment(context.Background(), emuOrg, emuRepo, emuIssueNumber, &github.IssueComment{
		Body: &newBody,
	})
	if err != nil {
		return err
	}
	return nil
}

func (m *Manager) deleteGitHubComment(webhook *types.WebHook) error {
	emuOrg, emuRepo, emuIssueNumber, err := m.DBClient.GetEMUCommentIDEntry(webhook)
	if err != nil {
		return err
	}

	client, err := m.retrieveInstallationClient(emuOrg)
	if err != nil {
		return err
	}
	_, err = client.Issues.DeleteComment(context.Background(), emuOrg, emuRepo, emuIssueNumber)
	if err != nil {
		return err
	}
	return nil
}

func (m *Manager) retrieveInstallationClient(org string) (*github.Client, error) {
	if client, exists := installationClients[org]; exists {
		return client, nil
	}
	client, err := m.createInstallationClient(org)
	if err != nil {
		return nil, err
	}
	return client, nil
}

func (m *Manager) createInstallationClient(org string) (*github.Client, error) {
	options := &github.ListOptions{
		Page:    0,
		PerPage: 100,
	}
	for {
		installations, _, err := m.Client.Apps.ListInstallations(context.Background(), options)
		if err != nil {
			return nil, err
		}
		for _, installation := range installations {
			if installation.GetAccount().GetLogin() == org && installation.GetID() != 21250525 {
				client, err := m.createNewInstallationClient(installation.GetID())
				if err != nil {
					return nil, err
				}
				installationClients[org] = client
				return client, nil
			}
		}
		if len(installations) < 100 {
			break
		}
		options.Page++
	}
	return nil, fmt.Errorf("no installation found for %s", org)
}

func (m *Manager) createNewInstallationClient(id int64) (*github.Client, error) {
	privateKey, err := base64.StdEncoding.DecodeString(m.Config.Apps.Client.PrivateKey)
	if err != nil {
		return nil, err
	}
	itr, err := ghinstallation.New(http.DefaultTransport, m.Config.Apps.Client.AppID, id, privateKey)
	client := github.NewClient(&http.Client{Transport: itr})
	return client, nil
}
