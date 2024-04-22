/*
Copyright 2017 The Kubernetes Authors.

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

package testutil

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakectrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type IndexableManager interface {
	GetClient() ctrlruntimeclient.Client
	GetFieldIndexer() ctrlruntimeclient.FieldIndexer
}

type fakeManager struct {
	client ctrlruntimeclient.Client
}

type IndexSetupFunc func(ctx context.Context, indexer ctrlruntimeclient.FieldIndexer) error

func NewFakeManager(ctx context.Context, objects []runtime.Object, indexSetups ...IndexSetupFunc) (*fakeManager, error) {
	// collect the indexes used within the fake manager
	collector := &fakeIndexer{
		indexFuncs: map[ctrlruntimeclient.Object]map[string]ctrlruntimeclient.IndexerFunc{},
	}

	for _, setup := range indexSetups {
		if err := setup(ctx, collector); err != nil {
			return nil, err
		}
	}

	builder := fakectrlruntimeclient.NewClientBuilder()
	builder.WithRuntimeObjects(objects...)

	for obj, indexes := range collector.indexFuncs {
		for field, extractFunc := range indexes {
			builder.WithIndex(obj, field, extractFunc)
		}
	}

	return &fakeManager{
		client: builder.Build(),
	}, nil
}

func (fm *fakeManager) GetClient() ctrlruntimeclient.Client {
	return fm.client
}

func (fm *fakeManager) GetFieldIndexer() ctrlruntimeclient.FieldIndexer {
	return fm
}

// IndexField does nothing because all indexes relevant to the test have been setup
// in newFakeManager() already.
func (fm *fakeManager) IndexField(_ context.Context, _ ctrlruntimeclient.Object, _ string, _ ctrlruntimeclient.IndexerFunc) error {
	return nil
}

type fakeIndexer struct {
	indexFuncs map[ctrlruntimeclient.Object]map[string]ctrlruntimeclient.IndexerFunc
}

func (fm *fakeIndexer) IndexField(_ context.Context, obj ctrlruntimeclient.Object, field string, extractValue ctrlruntimeclient.IndexerFunc) error {
	if _, ok := fm.indexFuncs[obj]; !ok {
		fm.indexFuncs[obj] = map[string]ctrlruntimeclient.IndexerFunc{}
	}
	fm.indexFuncs[obj][field] = extractValue
	return nil
}
