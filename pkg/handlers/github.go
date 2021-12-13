package handlers

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v41/github"
	"github.com/lindluni/github-issue-sync/pkg/db"
	"github.com/lindluni/github-issue-sync/pkg/types"
	"github.com/shurcooL/githubv4"
	"github.com/sirupsen/logrus"
)

type GitHub struct {
	Client        *github.Client
	DBClient      *db.Manager
	GitHubClient  *github.Client
	GraphQLClient *githubv4.Client

	Config *types.Config

	Logger *logrus.Logger
}

var installationClients = make(map[string]*github.Client)

func (g *GitHub) HandleIssue(webhook *types.WebHook) error {
	switch webhook.Action {
	case "edited":
		err := g.editIssue(webhook)
		if err != nil {
			return err
		}
	case "closed":
		err := g.updateIssueState(webhook)
		if err != nil {
			return err
		}
		err = g.DBClient.UpdateIssueEntry(webhook)
		if err != nil {
			return err
		}
	case "reopened":
		err := g.updateIssueState(webhook)
		if err != nil {
			return err
		}
		err = g.DBClient.UpdateIssueEntry(webhook)
		if err != nil {
			return err
		}
	}
	return nil
}

func (g *GitHub) editIssue(webhook *types.WebHook) error {
	if webhook.Changes.Title != nil && webhook.Changes.Body != nil {
		_, _, err := g.GitHubClient.Issues.Edit(context.Background(), g.Config.Repo.Org, g.Config.Repo.Name, webhook.Issue.GetNumber(), &github.IssueRequest{
			Title: webhook.Changes.GetTitle().From,
			Body:  webhook.Changes.GetBody().From,
		})
		return err
	}
	if webhook.Changes.Title != nil && webhook.Changes.Body == nil {
		_, _, err := g.GitHubClient.Issues.Edit(context.Background(), g.Config.Repo.Org, g.Config.Repo.Name, webhook.Issue.GetNumber(), &github.IssueRequest{
			Title: webhook.Changes.GetTitle().From,
		})
		return err
	}
	if webhook.Changes.Title == nil && webhook.Changes.Body != nil {
		_, _, err := g.GitHubClient.Issues.Edit(context.Background(), g.Config.Repo.Org, g.Config.Repo.Name, webhook.Issue.GetNumber(), &github.IssueRequest{
			Body: webhook.Changes.GetBody().From,
		})
		return err
	}
	return fmt.Errorf("unable to evaluate issue edit")
}

func (g *GitHub) updateIssueState(webhook *types.WebHook) error {
	org, repo, issueNumber, err := g.DBClient.GetEMUIssue(webhook)
	if err != nil {
		return err
	}
	client, err := g.retrieveInstallationClient(webhook.Installation.GetID())
	if err != nil {
		return err
	}
	_, _, err = client.Issues.Edit(context.Background(), org, repo, issueNumber, &github.IssueRequest{
		State: webhook.Issue.State,
	})
	return err
}

func (g *GitHub) HandleIssueComment(webhook *types.WebHook) error {
	switch webhook.Action {
	case "created":
		emuIssueID, emuCommentID, err := g.createComment(webhook)
		if err != nil {
			return err
		}
		err = g.DBClient.InsertGitHubCommentEntry(webhook, emuIssueID, emuCommentID)
		if err != nil {
			return err
		}
	case "edited":
		err := g.editComment(webhook)
		if err != nil {
			return err
		}
		err = g.DBClient.UpdateCommentEntry(webhook)
		if err != nil {
			return err
		}
	case "deleted":
		err := g.deleteComment(webhook)
		if err != nil {
			return err
		}
		err = g.DBClient.DeleteCommentEntry(webhook)
		if err != nil {
			return err
		}
	}
	return nil
}

func (g *GitHub) createComment(webhook *types.WebHook) (int64, int64, error) {
	emuIssueID, emuOrg, emuRepo, emuIssueNumber, err := g.DBClient.GetEMUIssueIDFromGitHubCommentEntry(webhook)
	if err != nil {
		return -1, -1, err
	}
	author := webhook.Comment.User.GetLogin()
	body := webhook.Comment.GetBody()

	newBody := fmt.Sprintf("@%s posted:\n\n%s", author, body)
	client, err := g.retrieveInstallationClient(webhook.Installation.GetID())
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

func (g *GitHub) editComment(webhook *types.WebHook) error {
	emuOrg, emuRepo, emuIssueNumber, err := g.DBClient.GetEMUCommentIDEntry(webhook)
	if err != nil {
		return err
	}
	author := webhook.Comment.User.GetLogin()
	body := webhook.Comment.GetBody()
	newBody := fmt.Sprintf("@%s posted:\n\n%s", author, body)

	client, err := g.retrieveInstallationClient(webhook.Installation.GetID())
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

func (g *GitHub) deleteComment(webhook *types.WebHook) error {
	emuOrg, emuRepo, emuIssueNumber, err := g.DBClient.GetEMUCommentIDEntry(webhook)
	if err != nil {
		return err
	}

	client, err := g.retrieveInstallationClient(webhook.Installation.GetID())
	if err != nil {
		return err
	}
	_, err = client.Issues.DeleteComment(context.Background(), emuOrg, emuRepo, emuIssueNumber)
	if err != nil {
		return err
	}
	return nil
}

func (g *GitHub) retrieveInstallationClient(id int64) (*github.Client, error) {
	privateKey, err := base64.StdEncoding.DecodeString(g.Config.Apps.Client.PrivateKey)
	if err != nil {
		return nil, err
	}
	itr, err := ghinstallation.New(http.DefaultTransport, g.Config.Apps.Client.AppID, id, privateKey)
	client := github.NewClient(&http.Client{Transport: itr})
	return client, nil
}
