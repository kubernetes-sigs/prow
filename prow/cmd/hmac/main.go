/*
Copyright 2020 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/yaml"

	"k8s.io/test-infra/pkg/flagutil"
	"k8s.io/test-infra/prow/config"
	prowflagutil "k8s.io/test-infra/prow/flagutil"
	configflagutil "k8s.io/test-infra/prow/flagutil/config"
	"k8s.io/test-infra/prow/ghhook"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/logrusutil"
)

type options struct {
	config configflagutil.ConfigOptions

	dryRun        bool
	github        prowflagutil.GitHubOptions
	kubernetes    prowflagutil.KubernetesOptions
	kubeconfigCtx string

	hookUrl                  string
	hmacTokenSecretNamespace string
	hmacTokenSecretName      string
	hmacTokenKey             string
}

func (o *options) validate() error {
	for _, group := range []flagutil.OptionGroup{&o.kubernetes, &o.github, &o.config} {
		if err := group.Validate(o.dryRun); err != nil {
			return err
		}
	}

	if o.kubeconfigCtx == "" {
		return errors.New("required flag --kubeconfig-context was unset")
	}
	if o.hookUrl == "" {
		return errors.New("required flag --hook-url was unset")
	}
	if o.hmacTokenSecretName == "" {
		return errors.New("required flag --hmac-token-secret-name was unset")
	}
	if o.hmacTokenKey == "" {
		return errors.New("required flag --hmac-token-key was unset")
	}

	return nil
}

func gatherOptions(fs *flag.FlagSet, args ...string) options {
	var o options

	o.config.AddFlags(fs)
	o.github.AddFlags(fs)
	o.kubernetes.AddFlags(fs)

	fs.StringVar(&o.kubeconfigCtx, "kubeconfig-context", "", "Context of the Prow component cluster and namespace in the kubeconfig.")
	fs.BoolVar(&o.dryRun, "dry-run", true, "Dry run for testing. Uses API tokens but does not mutate.")

	fs.StringVar(&o.hookUrl, "hook-url", "", "Prow hook external webhook URL (e.g. https://prow.k8s.io/hook).")
	fs.StringVar(&o.hmacTokenSecretNamespace, "hmac-token-secret-namespace", "default", "Name of the namespace on the cluster where the hmac-token secret is in.")
	fs.StringVar(&o.hmacTokenSecretName, "hmac-token-secret-name", "", "Name of the secret on the cluster containing the GitHub HMAC secret.")
	fs.StringVar(&o.hmacTokenKey, "hmac-token-key", "", "Key of the hmac token in the secret.")
	fs.Parse(args)
	return o
}

type client struct {
	options options

	kubernetesClient kubernetes.Interface
	githubHookClient github.HookClient

	currentHMACMap map[string]github.HMACsForRepo
	newHMACConfig  config.ManagedWebhooks

	hmacMapForBatchUpdate map[string]string
	hmacMapForRecovery    map[string]github.HMACsForRepo
}

func main() {
	logrusutil.ComponentInit()

	o := gatherOptions(flag.NewFlagSet(os.Args[0], flag.ExitOnError), os.Args[1:]...)
	if err := o.validate(); err != nil {
		logrus.WithError(err).Fatal("Invalid options")
	}

	kc, err := o.kubernetes.ClusterClientForContext(o.kubeconfigCtx, o.dryRun)
	if err != nil {
		logrus.WithError(err).Fatalf("Error creating Kubernetes client for cluster %q.", o.kubeconfigCtx)
	}

	configAgent, err := o.config.ConfigAgent()
	if err != nil {
		logrus.WithError(err).Fatal("Error starting config agent.")
	}
	newHMACConfig := configAgent.Config().ManagedWebhooks

	gc, err := o.github.GitHubClient(o.dryRun)
	if err != nil {
		logrus.WithError(err).Fatal("Error creating github client")
	}

	currentHMACYaml, err := getCurrentHMACTokens(kc, o.hmacTokenSecretNamespace, o.hmacTokenSecretName, o.hmacTokenKey)
	if err != nil {
		logrus.WithError(err).Fatal("Error getting the current hmac yaml.")
	}

	currentHMACMap := map[string]github.HMACsForRepo{}
	if err := yaml.Unmarshal(currentHMACYaml, &currentHMACMap); err != nil {
		// When the token is still a single global token, respect_legacy_global_token must be set to true before running this tool.
		// This can prevent the global token from being deleted by mistake before users migrate all repos/orgs to use auto-generated private tokens.
		if !newHMACConfig.RespectLegacyGlobalToken {
			logrus.Fatal("respect_legacy_global_token must be set to true before the hmac tool is run for the first time.")
		}

		logrus.WithError(err).Error("Couldn't unmarshal the hmac secret as hierarchical file. Parsing as a single global token and writing it back to the secret.")
		currentHMACMap["*"] = github.HMACsForRepo{
			github.HMACToken{
				Value: strings.TrimSpace(string(currentHMACYaml)),
			},
		}
	}

	c := client{
		kubernetesClient: kc,
		githubHookClient: gc,
		options:          o,

		currentHMACMap:        currentHMACMap,
		newHMACConfig:         newHMACConfig,
		hmacMapForBatchUpdate: map[string]string{},
		hmacMapForRecovery:    map[string]github.HMACsForRepo{},
	}

	if err := c.handleInvitation(); err != nil {
		logrus.WithError(err).Fatal("Error accepting invitations.")
	}

	if err := c.handleConfigUpdate(); err != nil {
		logrus.WithError(err).Fatal("Error handling hmac config update.")
	}
}

func (c *client) handleInvitation() error {
	if !c.newHMACConfig.AutoAcceptInvitation {
		logrus.Debug("Skip accepting github invitations as not configured.")
		return nil
	}
	// Accept repos invitations first
	repoIvs, err := c.githubHookClient.ListCurrentUserRepoInvitations()
	if err != nil {
		return err
	}
	for _, iv := range repoIvs {
		if iv.Permission != github.Admin {
			logrus.Errorf("invalid invitation from %s is not accepted. Permission want: %v, got: %s",
				iv.Repository.FullName, github.Admin, iv.Permission)
			continue
		}
		for repoName := range c.newHMACConfig.OrgRepoConfig {
			// Only consider strict matching for repo level invitation,
			// reasons for not considering org matching:
			// 1. The FullName is org/repo
			// 2. If an org is defined as managed webhook but only invite
			// bot as admin on repo level, the webhook setup will fail
			// 3. Also we are not ready to receive spamming webhook from the
			// org if it only configured a repo in hmac
			if iv.Repository.FullName == repoName {
				if err := c.githubHookClient.AcceptUserRepoInvitation(iv.InvitationID); err != nil {
					return fmt.Errorf("failed accepting repo invitation from %s: %w", iv.Repository.FullName, err)
				}
				logrus.Infof("Successfully accepted invitation from %s", iv.Repository.FullName)
			}
		}
	}
	// Accept org invitation
	orgIvs, err := c.githubHookClient.ListCurrentUserOrgInvitations()
	if err != nil {
		return err
	}
	for _, iv := range orgIvs {
		if iv.Role != github.OrgAdmin {
			logrus.Errorf("Invalid invitation from %s not accepted. Want: %v, got: %s",
				iv.Org.Login, github.Admin, iv.Role)
			continue
		}
		for repoName := range c.newHMACConfig.OrgRepoConfig {
			// Accept org invitation even if only single repo want hmac
			if repoName == iv.Org.Login || strings.HasPrefix(repoName, iv.Org.Login+"/") {
				if err := c.githubHookClient.AcceptUserOrgInvitation(iv.Org.Login); err != nil {
					return fmt.Errorf("failed accepting org invitation from %s: %w", iv.Org.Login, err)
				}
				logrus.Infof("Successfully accepted invitation from %s", iv.Org.Login)
			}
		}
	}

	return nil
}

func (c *client) handleConfigUpdate() error {
	repoAdded := map[string]config.ManagedWebhookInfo{}
	repoRemoved := map[string]bool{}
	repoRotated := map[string]config.ManagedWebhookInfo{}

	for repoName, hmacConfig := range c.newHMACConfig.OrgRepoConfig {
		if _, ok := c.currentHMACMap[repoName]; ok {
			repoRotated[repoName] = hmacConfig
		} else {
			repoAdded[repoName] = hmacConfig
		}
	}

	for repoName := range c.currentHMACMap {
		// Skip the global hmac token if it still needs to be respected.
		if repoName == "*" && c.newHMACConfig.RespectLegacyGlobalToken {
			continue
		}
		if _, ok := c.newHMACConfig.OrgRepoConfig[repoName]; !ok {
			repoRemoved[repoName] = true
		}
	}

	// Remove the webhooks for the given repos, as well as removing the tokens from the current hmac map.
	if err := c.handleRemovedRepo(repoRemoved); err != nil {
		return fmt.Errorf("error handling hmac update for removed repos: %w", err)
	}

	// Generate new hmac token for required repos, do batch update for the hmac token secret,
	// and then iteratively update the webhook for each repo.
	if err := c.handleAddedRepo(repoAdded); err != nil {
		return fmt.Errorf("error handling hmac update for new repos: %w", err)
	}
	if err := c.handledRotatedRepo(repoRotated); err != nil {
		return fmt.Errorf("error handling hmac rotations for the repos: %w", err)
	}
	// Update the hmac token secret first, to guarantee the new tokens are available to hook.
	if err := c.updateHMACTokenSecret(); err != nil {
		return fmt.Errorf("error updating hmac tokens: %w", err)
	}
	// HACK: waiting for the hmac k8s secret update to propagate to the pods that are using the secret,
	// so that components like hook can start respecting the new hmac values.
	time.Sleep(20 * time.Second)
	errs := c.batchOnboardNewTokenForRepos()

	// Do necessary cleanups after the token and webhook updates are done.
	if err := c.cleanup(); err != nil {
		errs = append(errs, fmt.Errorf("error cleaning up %w", err))
	}

	return utilerrors.NewAggregate(errs)
}

// handleRemoveRepo handles webhook removal and hmac token removal from the current hmac map for all repos removed from the declarative config.
func (c *client) handleRemovedRepo(removed map[string]bool) error {
	removeGlobalToken := false
	repos := make([]string, 0)
	for r := range removed {
		if r == "*" {
			removeGlobalToken = true
		} else {
			repos = append(repos, r)
		}
	}

	if len(repos) != 0 {
		o := ghhook.Options{
			GitHubOptions:    c.options.github,
			GitHubHookClient: c.githubHookClient,
			Repos:            prowflagutil.NewStrings(repos...),
			HookURL:          c.options.hookUrl,
			ShouldDelete:     true,
			Confirm:          true,
		}
		if err := o.Validate(); err != nil {
			return fmt.Errorf("error validating the options: %w", err)
		}

		logrus.WithField("repos", repos).Debugf("Deleting webhooks for %q", c.options.hookUrl)
		if err := o.HandleWebhookConfigChange(); err != nil {
			return fmt.Errorf("error deleting webhook for repos %q: %w", repos, err)
		}

		for _, repo := range repos {
			delete(c.currentHMACMap, repo)
		}
	}

	if removeGlobalToken {
		delete(c.currentHMACMap, "*")
	}
	// No need to update the secret here, the following update will commit the changes together.

	return nil
}

func (c *client) handleAddedRepo(added map[string]config.ManagedWebhookInfo) error {
	for repo := range added {
		if err := c.addRepoToBatchUpdate(repo); err != nil {
			return err
		}
	}
	return nil
}

func (c *client) handledRotatedRepo(rotated map[string]config.ManagedWebhookInfo) error {
	// For each rotated repo, we only onboard a new token when none of the existing tokens is created after user specified time.
	for repo, hmacConfig := range rotated {
		needsRotation := true
		for _, token := range c.currentHMACMap[repo] {
			// If the existing token is created after the user specified time, we do not need to rotate it.
			if token.CreatedAt.After(hmacConfig.TokenCreatedAfter) {
				needsRotation = false
				break
			}
		}
		if needsRotation {
			if err := c.addRepoToBatchUpdate(repo); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *client) addRepoToBatchUpdate(repo string) error {
	generatedToken, err := generateNewHMACToken()
	if err != nil {
		return fmt.Errorf("error generating a new hmac token for %q: %w", repo, err)
	}

	updatedTokenList := github.HMACsForRepo{}
	// Copy over all existing tokens for that repo, if it's already been configured.
	if val, ok := c.currentHMACMap[repo]; ok {
		updatedTokenList = append(updatedTokenList, val...)
		// Back up the hmacs for the current repo, which we can use for recovery in case an error happens in updating the webhook.
		c.hmacMapForRecovery[repo] = c.currentHMACMap[repo]
		// Current webhook is possibly using global token so we need to promote that token to repo level, if it exists.
	} else if globalTokens, ok := c.currentHMACMap["*"]; ok {
		updatedTokenList = append(updatedTokenList, github.HMACToken{
			Value: globalTokens[0].Value,
			// Set CreatedAt as a time slightly before the TokenCreatedAfter time, so that the token can be properly pruned in the end.
			CreatedAt: c.newHMACConfig.OrgRepoConfig[repo].TokenCreatedAfter.Add(-time.Second),
		})
	}

	updatedTokenList = append(updatedTokenList, github.HMACToken{
		Value: generatedToken, CreatedAt: time.Now()})
	c.currentHMACMap[repo] = updatedTokenList
	c.hmacMapForBatchUpdate[repo] = generatedToken

	return nil
}

func (c *client) onboardNewTokenForRepo(repo, generatedToken string) error {
	// Update the github webhook to use new token.
	o := ghhook.Options{
		GitHubOptions:    c.options.github,
		GitHubHookClient: c.githubHookClient,
		Repos:            prowflagutil.NewStrings(repo),
		HookURL:          c.options.hookUrl,
		HMACValue:        generatedToken,
		// Receive hooks for all the events.
		Events:  prowflagutil.NewStrings(github.AllHookEvents...),
		Confirm: true,
	}
	if err := o.Validate(); err != nil {
		return fmt.Errorf("error validating the options: %w", err)
	}

	logrus.WithField("repo", repo).Debugf("Updating the webhook for %q", c.options.hookUrl)
	return o.HandleWebhookConfigChange()
}

func (c *client) batchOnboardNewTokenForRepos() []error {
	var errs []error
	for repo, generatedToken := range c.hmacMapForBatchUpdate {
		if err := c.onboardNewTokenForRepo(repo, generatedToken); err != nil {
			errs = append(errs, err)
			logrus.WithError(err).Errorf("Error updating the webhook, will revert the hmacs for %q", repo)
			if hmacs, exist := c.hmacMapForRecovery[repo]; exist {
				c.currentHMACMap[repo] = hmacs
			} else {
				delete(c.currentHMACMap, repo)
			}
		}
	}
	return errs
}

// cleanup will do necessary cleanups after the token and webhook updates are done.
func (c *client) cleanup() error {
	// Prune old tokens from current config.
	for repoName := range c.currentHMACMap {
		c.pruneOldTokens(repoName)
	}
	// Update the secret.
	if err := c.updateHMACTokenSecret(); err != nil {
		return fmt.Errorf("error updating hmac tokens: %w", err)
	}
	return nil
}

// updateHMACTokenSecret saves given in-memory config to secret file used by prow cluster.
func (c *client) updateHMACTokenSecret() error {
	if c.options.dryRun {
		logrus.Debug("dryrun option is enabled, updateHMACTokenSecret won't actually update the secret.")
		return nil
	}

	secretContent, err := yaml.Marshal(&c.currentHMACMap)
	if err != nil {
		return fmt.Errorf("error converting hmac map to yaml: %w", err)
	}
	sec := &corev1.Secret{}
	sec.Name = c.options.hmacTokenSecretName
	sec.Namespace = c.options.hmacTokenSecretNamespace
	sec.StringData = map[string]string{c.options.hmacTokenKey: string(secretContent)}
	if _, err = c.kubernetesClient.CoreV1().Secrets(c.options.hmacTokenSecretNamespace).Update(context.TODO(), sec, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("error updating the secret: %w", err)
	}
	return nil
}

// pruneOldTokens removes all but most recent token from token config.
func (c *client) pruneOldTokens(repo string) {
	tokens := c.currentHMACMap[repo]
	if len(tokens) <= 1 {
		logrus.WithField("repo", repo).Debugf("Token size is %d, no need to prune", len(tokens))
		return
	}

	logrus.WithField("repo", repo).Debugf("Token size is %d, prune to 1", len(tokens))
	sort.SliceStable(tokens, func(i, j int) bool {
		return tokens[i].CreatedAt.After(tokens[j].CreatedAt)
	})
	c.currentHMACMap[repo] = tokens[:1]
}

// generateNewHMACToken generates a hex encoded crypto random string of length 40.
func generateNewHMACToken() (string, error) {
	bytes := make([]byte, 20) // 20 bytes of entropy will result in a string of length 40 after hex encoding
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// getCurrentHMACTokens returns the hmac tokens currently configured in the cluster.
func getCurrentHMACTokens(kc kubernetes.Interface, ns, secName, key string) ([]byte, error) {
	sec, err := kc.CoreV1().Secrets(ns).Get(context.TODO(), secName, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("error getting hmac secret %q: %w", secName, err)
	}
	if err == nil {
		buf, ok := sec.Data[key]
		if ok {
			return buf, nil
		}
		return nil, fmt.Errorf("error getting key %q from the hmac secret %q", key, secName)
	}
	return nil, fmt.Errorf("error getting hmac token values: %w", err)
}
