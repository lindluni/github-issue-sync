package server

// TODO: Verify EMU repo is a known repo

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/lindluni/github-issue-sync/pkg/db"
	"github.com/lindluni/github-issue-sync/pkg/types"
	"github.com/shurcooL/githubv4"

	//"github.com/gin-contrib/requestid"
	"github.com/gin-gonic/gin"
	"github.com/google/go-github/v41/github"
	"github.com/sirupsen/logrus"
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
	switch event {
	case "issues":
		webhook, err := parseWebHook(c)
		if err != nil {
			fmt.Println(err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if webhook.Issue.User.GetLogin() != m.Config.Apps.BotName {
			err = m.handleEMUIssue(webhook)
			if err != nil {
				fmt.Println(err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
		}
		break
	case "issue_comment":
		webhook, err := parseWebHook(c)
		if err != nil {
			fmt.Println(err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if webhook.Comment.User.GetLogin() != m.Config.Apps.BotName {
			err = m.handleEMUIssueComment(webhook)
			if err != nil {
				fmt.Println(err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
		}
		break
	default:
		fmt.Printf("Unsupported event: %s\n", event)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Unsupported event"})
		return
	}
}

func (m *Manager) DoWebHookGitHub(c *gin.Context) {
	//event := c.GetHeader("X-GitHub-Event")
	//switch event {
	//case "issues":
	//	//handle reversing deletion and stuff
	//	fmt.Println("Handled issue event")
	//	break
	//case "issue_comment":
	//	webhook, err := parseWebHook(c)
	//	if err != nil {
	//		fmt.Println(err)
	//		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	//		return
	//	}
	//	if webhook.Comment.User.GetLogin() != m.Config.Apps.BotName {
	//		err = m.handleEMUIssueComment(webhook)
	//		if err != nil {
	//			fmt.Println(err)
	//			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	//			return
	//		}
	//	}
	//	break
	//default:
	//	fmt.Printf("Unsupported event: %s\n", event)
	//	c.JSON(http.StatusOK, gin.H{"Error": "Unsupported event"})
	//}
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
		issue, err := m.openIssue(webhook)
		if err != nil {
			return err
		}
		err = m.DBClient.InsertIssueEntry(webhook, issue.GetNumber())
		if err != nil {
			return err
		}
	case "edited":
		err := m.editIssue(webhook)
		if err != nil {
			return err
		}
		err = m.DBClient.UpdateIssueEntry(webhook)
		if err != nil {
			return err
		}
	case "deleted":
		err := m.deleteIssue(webhook)
		if err != nil {
			return err
		}
		err = m.DBClient.DeleteIssueEntry(webhook)
		if err != nil {
			return err
		}
	case "closed":
		err := m.updateIssueState(webhook)
		if err != nil {
			return err
		}
		err = m.DBClient.UpdateIssueEntry(webhook)
		if err != nil {
			return err
		}
	case "reopened":
		err := m.updateIssueState(webhook)
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

func (m *Manager) openIssue(webhook *types.WebHook) (*github.Issue, error) {
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

func (m *Manager) editIssue(webhook *types.WebHook) error {
	org := webhook.Repository.Owner.GetLogin()
	repo := webhook.Repository.GetName()
	issueNumber := webhook.Issue.GetNumber()
	title := webhook.Issue.GetTitle()
	author := webhook.Issue.User.GetLogin()
	body := webhook.Issue.GetBody()

	syncedIssueNumber, err := m.DBClient.GetSyncedIssueIDEntry(webhook)
	if err != nil {
		return err
	}

	newTitle := fmt.Sprintf("%s/%s#%d: %s", org, repo, issueNumber, title)
	newBody := fmt.Sprintf("@%s posted:\n\n%s", author, body)

	_, _, err = m.GitHubClient.Issues.Edit(context.Background(), m.Config.Repo.Org, m.Config.Repo.Name, syncedIssueNumber, &github.IssueRequest{
		Title: &newTitle,
		Body:  &newBody,
	})

	return err
}

func (m *Manager) deleteIssue(webhook *types.WebHook) error {
	syncedIssueNumber, err := m.DBClient.GetSyncedIssueIDEntry(webhook)
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

	issue, _, err := m.GitHubClient.Issues.Get(context.Background(), m.Config.Repo.Org, m.Config.Repo.Name, syncedIssueNumber)
	input := githubv4.DeleteIssueInput{
		IssueID: issue.GetNodeID(),
	}

	err = m.GraphQLClient.Mutate(context.Background(), &mutation, input, nil)
	if err != nil {
		return err
	}
	return nil
}

func (m *Manager) updateIssueState(webhook *types.WebHook) error {
	syncedIssueNumber, err := m.DBClient.GetSyncedIssueIDEntry(webhook)
	if err != nil {
		return err
	}
	_, _, err = m.GitHubClient.Issues.Edit(context.Background(), m.Config.Repo.Org, m.Config.Repo.Name, syncedIssueNumber, &github.IssueRequest{
		State: webhook.Issue.State,
	})

	return err
}

func (m *Manager) handleEMUIssueComment(webhook *types.WebHook) error {
	switch webhook.Action {
	case "created":
		id, err := m.createComment(webhook)
		if err != nil {
			return err
		}
		err = m.DBClient.InsertCommentEntry(webhook, id)
		if err != nil {
			return err
		}
		break
	case "edited":
		err := m.editComment(webhook)
		if err != nil {
			return err
		}
		err = m.DBClient.UpdateCommentEntry(webhook)
		if err != nil {
			return err
		}
		break
	case "deleted":
		err := m.deleteComment(webhook)
		if err != nil {
			return err
		}
		err = m.DBClient.DeleteCommentEntry(webhook)
		if err != nil {
			return err
		}
		break
	}
	return nil
}

func (m *Manager) createComment(webhook *types.WebHook) (int64, error) {
	syncedIssueNumber, err := m.DBClient.GetSyncedIssueIDEntry(webhook)
	if err != nil {
		return -1, err
	}
	author := webhook.Comment.User.GetLogin()
	body := webhook.Comment.GetBody()

	newBody := fmt.Sprintf("@%s posted:\n\n%s", author, body)

	comment, _, err := m.GitHubClient.Issues.CreateComment(context.Background(), m.Config.Repo.Org, m.Config.Repo.Name, syncedIssueNumber, &github.IssueComment{
		Body: &newBody,
	})
	if err != nil {
		return -1, err
	}
	return comment.GetID(), nil
}

func (m *Manager) editComment(webhook *types.WebHook) error {
	syncedIssueNumber, err := m.DBClient.GetSyncedCommentIDEntry(webhook)
	if err != nil {
		return err
	}
	author := webhook.Comment.User.GetLogin()
	body := webhook.Comment.GetBody()

	newBody := fmt.Sprintf("@%s posted:\n\n%s", author, body)
	_, _, err = m.GitHubClient.Issues.EditComment(context.Background(), m.Config.Repo.Org, m.Config.Repo.Name, int64(syncedIssueNumber), &github.IssueComment{
		Body: &newBody,
	})
	if err != nil {
		return err
	}
	return nil
}

func (m *Manager) deleteComment(webhook *types.WebHook) error {
	syncedIssueNumber, err := m.DBClient.GetSyncedCommentIDEntry(webhook)
	if err != nil {
		return err
	}

	_, err = m.GitHubClient.Issues.DeleteComment(context.Background(), m.Config.Repo.Org, m.Config.Repo.Name, int64(syncedIssueNumber))
	if err != nil {
		return err
	}
	return nil
}
