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
)

type GitHub struct{}

var installationClients = make(map[string]*github.Client)

func (g *GitHub) HandleIssue(webhook *types.WebHook, rawClient *github.Client, dbClient *db.Manager, githubClient *github.Client, config *types.Config) error {
	switch webhook.Action {
	case "edited":
		err := g.editIssue(webhook, githubClient, config)
		if err != nil {
			return err
		}
	case "closed":
		err := g.updateIssueState(webhook, dbClient, rawClient, config)
		if err != nil {
			return err
		}
		err = dbClient.UpdateIssueEntry(webhook)
		if err != nil {
			return err
		}
	case "reopened":
		err := g.updateIssueState(webhook, dbClient, rawClient, config)
		if err != nil {
			return err
		}
		err = dbClient.UpdateIssueEntry(webhook)
		if err != nil {
			return err
		}
	}
	return nil
}

func (g *GitHub) editIssue(webhook *types.WebHook, githubClient *github.Client, config *types.Config) error {
	if webhook.Changes.Title != nil && webhook.Changes.Body != nil {
		_, _, err := githubClient.Issues.Edit(context.Background(), config.Repo.Org, config.Repo.Name, webhook.Issue.GetNumber(), &github.IssueRequest{
			Title: webhook.Changes.GetTitle().From,
			Body:  webhook.Changes.GetBody().From,
		})
		return err
	}
	if webhook.Changes.Title != nil && webhook.Changes.Body == nil {
		_, _, err := githubClient.Issues.Edit(context.Background(), config.Repo.Org, config.Repo.Name, webhook.Issue.GetNumber(), &github.IssueRequest{
			Title: webhook.Changes.GetTitle().From,
		})
		return err
	}
	if webhook.Changes.Title == nil && webhook.Changes.Body != nil {
		_, _, err := githubClient.Issues.Edit(context.Background(), config.Repo.Org, config.Repo.Name, webhook.Issue.GetNumber(), &github.IssueRequest{
			Body: webhook.Changes.GetBody().From,
		})
		return err
	}
	return fmt.Errorf("unable to evaluate issue edit")
}

func (g *GitHub) updateIssueState(webhook *types.WebHook, dbClient *db.Manager, rawClient *github.Client, config *types.Config) error {
	org, repo, issueNumber, err := dbClient.GetEMUIssue(webhook)
	if err != nil {
		return err
	}
	client, err := g.retrieveInstallationClient(org, rawClient, config)
	if err != nil {
		return err
	}
	_, _, err = client.Issues.Edit(context.Background(), org, repo, issueNumber, &github.IssueRequest{
		State: webhook.Issue.State,
	})
	return err
}

func (g *GitHub) HandleIssueComment(webhook *types.WebHook, rawClient *github.Client, dbClient *db.Manager, config *types.Config) error {
	switch webhook.Action {
	case "created":
		emuIssueID, emuCommentID, err := g.createComment(webhook, dbClient, rawClient, config)
		if err != nil {
			return err
		}
		err = dbClient.InsertGitHubCommentEntry(webhook, emuIssueID, emuCommentID)
		if err != nil {
			return err
		}
	case "edited":
		err := g.editComment(webhook, dbClient, rawClient, config)
		if err != nil {
			return err
		}
		err = dbClient.UpdateCommentEntry(webhook)
		if err != nil {
			return err
		}
	case "deleted":
		err := g.deleteComment(webhook, dbClient, rawClient, config)
		if err != nil {
			return err
		}
		err = dbClient.DeleteCommentEntry(webhook)
		if err != nil {
			return err
		}
	}
	return nil
}

func (g *GitHub) createComment(webhook *types.WebHook, dbClient *db.Manager, rawClient *github.Client, config *types.Config) (int64, int64, error) {
	emuIssueID, emuOrg, emuRepo, emuIssueNumber, err := dbClient.GetEMUIssueIDFromGitHubCommentEntry(webhook)
	if err != nil {
		return -1, -1, err
	}
	author := webhook.Comment.User.GetLogin()
	body := webhook.Comment.GetBody()

	newBody := fmt.Sprintf("@%s posted:\n\n%s", author, body)
	client, err := g.retrieveInstallationClient(emuOrg, rawClient, config)
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

func (g *GitHub) editComment(webhook *types.WebHook, dbClient *db.Manager, rawClient *github.Client, config *types.Config) error {
	emuOrg, emuRepo, emuIssueNumber, err := dbClient.GetEMUCommentIDEntry(webhook)
	if err != nil {
		return err
	}
	author := webhook.Comment.User.GetLogin()
	body := webhook.Comment.GetBody()
	newBody := fmt.Sprintf("@%s posted:\n\n%s", author, body)

	client, err := g.retrieveInstallationClient(emuOrg, rawClient, config)
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

func (g *GitHub) deleteComment(webhook *types.WebHook, dbClient *db.Manager, rawClient *github.Client, config *types.Config) error {
	emuOrg, emuRepo, emuIssueNumber, err := dbClient.GetEMUCommentIDEntry(webhook)
	if err != nil {
		return err
	}

	client, err := g.retrieveInstallationClient(emuOrg, rawClient, config)
	if err != nil {
		return err
	}
	_, err = client.Issues.DeleteComment(context.Background(), emuOrg, emuRepo, emuIssueNumber)
	if err != nil {
		return err
	}
	return nil
}

func (g *GitHub) retrieveInstallationClient(org string, rawClient *github.Client, config *types.Config) (*github.Client, error) {
	if client, exists := installationClients[org]; exists {
		return client, nil
	}
	client, err := g.createInstallationClient(org, rawClient, config)
	if err != nil {
		return nil, err
	}
	return client, nil
}

func (g *GitHub) createInstallationClient(org string, rawClient *github.Client, config *types.Config) (*github.Client, error) {
	options := &github.ListOptions{
		Page:    0,
		PerPage: 100,
	}
	for {
		installations, _, err := rawClient.Apps.ListInstallations(context.Background(), options)
		if err != nil {
			return nil, err
		}
		for _, installation := range installations {
			if installation.GetAccount().GetLogin() == org && installation.GetID() != 21250525 {
				client, err := g.createNewInstallationClient(installation.GetID(), config)
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

func (g *GitHub) createNewInstallationClient(id int64, config *types.Config) (*github.Client, error) {
	privateKey, err := base64.StdEncoding.DecodeString(config.Apps.Client.PrivateKey)
	if err != nil {
		return nil, err
	}
	itr, err := ghinstallation.New(http.DefaultTransport, config.Apps.Client.AppID, id, privateKey)
	client := github.NewClient(&http.Client{Transport: itr})
	return client, nil
}
