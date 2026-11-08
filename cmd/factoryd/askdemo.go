package main

import (
	"context"
	"fmt"
	"time"

	"github.com/pablogore/software-factory/internal/adapters/out/memstore"
	"github.com/pablogore/software-factory/internal/domain"
	"github.com/pablogore/software-factory/internal/platform/jobtoken"
	"github.com/pablogore/software-factory/internal/slices/worker"
)

// demoAskJob waits in spec for a spec-writer, who may ask a person a
// question with ask_human; its pipeline is demoAskPipeline.
const demoAskJob domain.JobID = "demo-ask"

const demoAskPipeline = "demo-ask"

// demoSpecWriter has the tools roles/spec-writer.yaml lists.
var demoSpecWriter = domain.Role{Name: "spec-writer", Kind: domain.KindSpec, Tools: []string{"submit_result", "progress", "ask_human"}}

// answerPath is the demo's own route that answers a job's question as an
// allowed person would, with the request's body as the text, and prints the
// token and the brief the stage that asked runs again with.
const answerPath = "/demo/jobs/{job}/answer"

// demoAsk is a spec stage followed by a build, so a spec-writer's question
// waits in needs-info.
func demoAsk() domain.Pipeline {
	return domain.Pipeline{Name: demoAskPipeline, Stages: []domain.Stage{
		{Name: "spec", Kind: domain.KindSpec, Roles: []string{"spec-writer"}},
		{Name: "build", Kind: domain.KindBuild, Roles: []string{"builder"}},
	}}
}

// createAskJob creates demoAskJob in spec and returns a spec-writer's and a
// builder's token for it.
func createAskJob(ctx context.Context, store *memstore.Store, tokens *jobtoken.Tokens) (string, string, error) {
	job := domain.Job{ID: demoAskJob, Title: "Demo question", Pipeline: demoAskPipeline, Stage: "spec", Round: 1}
	if err := store.Create(ctx, job); err != nil {
		return "", "", err
	}
	writer, err := tokens.Issue(jobtoken.For(job, nil, demoSpecWriter))
	if err != nil {
		return "", "", err
	}
	builder, err := tokens.Issue(jobtoken.For(job, nil, demoBuilder))
	if err != nil {
		return "", "", err
	}
	return writer, builder, nil
}

// answer answers job id's current wait with text, as an allowed person's
// message, and returns a spec-writer's token for the spec stage it went back
// to and the brief that stage's agent is prompted with.
func answer(ctx context.Context, store *memstore.Store, tokens *jobtoken.Tokens, id domain.JobID, text string) (string, string, error) {
	job, v, err := store.Load(ctx, id)
	if err != nil {
		return "", "", err
	}
	p, err := demoPipelines{}.Pipeline(ctx, job.Pipeline)
	if err != nil {
		return "", "", err
	}
	msg := domain.MessageReceived{Job: id, Provider: "demo", Thread: string(id), Author: "demo-person", AuthorKind: domain.AuthorPerson,
		Allowed: true, Text: text, WaitSeq: job.WaitSeq, At: time.Now()}
	if _, _, err := store.Apply(ctx, id, v, p, msg); err != nil {
		return "", "", err
	}
	if job, _, err = store.Load(ctx, id); err != nil {
		return "", "", err
	}
	hist, err := store.History(ctx, id)
	if err != nil {
		return "", "", err
	}
	token, err := tokens.Issue(jobtoken.For(job, hist, demoSpecWriter))
	if err != nil {
		return "", "", err
	}
	brief, ok := worker.AnswerBrief(hist)
	if !ok {
		return "", "", fmt.Errorf("job %s has no answer in its history", id)
	}
	return token, brief, nil
}
