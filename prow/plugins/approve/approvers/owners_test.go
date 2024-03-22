/*
Copyright 2016 The Kubernetes Authors.

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

package approvers

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/sets"

	"k8s.io/test-infra/prow/pkg/layeredsets"
	"k8s.io/test-infra/prow/plugins/ownersconfig"
)

const (
	TestSeed = int64(0)
)

type FakeRepo struct {
	approversMap                 map[string]layeredsets.String
	leafApproversMap             map[string]sets.Set[string]
	noParentOwnersMap            map[string]bool
	autoApproveUnownedSubfolders map[string]bool
}

func (f FakeRepo) Filenames() ownersconfig.Filenames {
	return ownersconfig.FakeFilenames
}

func (f FakeRepo) Approvers(path string) layeredsets.String {
	ret := f.approversMap[path]
	if ret.Len() > 0 || path == "" {
		return ret
	}

	p := filepath.Dir(path)
	if p == "." {
		p = ""
	}
	return f.Approvers(p)
}

func (f FakeRepo) LeafApprovers(path string) sets.Set[string] {
	ret := f.leafApproversMap[path]

	if ret.Len() > 0 || path == "" {
		return ret
	}

	p := filepath.Dir(path)
	if p == "." {
		p = ""
	}
	return f.LeafApprovers(p)
}

func (f FakeRepo) FindApproverOwnersForFile(path string) string {
	for dir := path; dir != "."; dir = filepath.Dir(dir) {
		if _, ok := f.leafApproversMap[dir]; ok {
			return dir
		}
	}
	return ""
}

func (f FakeRepo) IsNoParentOwners(path string) bool {
	return f.noParentOwnersMap[path]
}

func (f FakeRepo) IsAutoApproveUnownedSubfolders(ownerFilePath string) bool {
	return f.autoApproveUnownedSubfolders[ownerFilePath]
}

func canonicalize(path string) string {
	if path == "." {
		return ""
	}
	return strings.TrimSuffix(path, "/")
}

func createFakeRepo(leafApproversMap map[string]sets.Set[string], modify ...func(*FakeRepo)) FakeRepo {
	// github doesn't use / at the root
	a := map[string]layeredsets.String{}
	for dir, approvers := range leafApproversMap {
		leafApproversMap[dir] = setToLower(approvers)
		a[dir] = setToLowerMulti(approvers)
		startingPath := dir
		for {
			dir = canonicalize(filepath.Dir(dir))
			if parentApprovers, ok := leafApproversMap[dir]; ok {
				a[startingPath] = a[startingPath].Union(setToLowerMulti(parentApprovers))
			}
			if dir == "" {
				break
			}
		}
	}

	fr := FakeRepo{approversMap: a, leafApproversMap: leafApproversMap}
	for _, m := range modify {
		m(&fr)
	}
	return fr
}

func setToLower(s sets.Set[string]) sets.Set[string] {
	lowered := sets.New[string]()
	for _, elem := range sets.List(s) {
		lowered.Insert(strings.ToLower(elem))
	}
	return lowered
}

func setToLowerMulti(s sets.Set[string]) layeredsets.String {
	lowered := layeredsets.NewString()
	for _, elem := range sets.List(s) {
		lowered.Insert(0, strings.ToLower(elem))
	}
	return lowered
}

func TestCreateFakeRepo(t *testing.T) {
	rootApprovers := sets.New[string]("Alice", "Bob")
	aApprovers := sets.New[string]("Art", "Anne")
	bApprovers := sets.New[string]("Bill", "Ben", "Barbara")
	cApprovers := sets.New[string]("Chris", "Carol")
	eApprovers := sets.New[string]("Eve", "Erin")
	edcApprovers := eApprovers.Union(cApprovers)
	FakeRepoMap := map[string]sets.Set[string]{
		"":        rootApprovers,
		"a":       aApprovers,
		"b":       bApprovers,
		"c":       cApprovers,
		"a/combo": edcApprovers,
	}
	fakeRepo := createFakeRepo(FakeRepoMap)

	tests := []struct {
		testName              string
		ownersFile            string
		expectedLeafApprovers sets.Set[string]
		expectedApprovers     sets.Set[string]
	}{
		{
			testName:              "Root Owners",
			ownersFile:            "",
			expectedApprovers:     rootApprovers,
			expectedLeafApprovers: rootApprovers,
		},
		{
			testName:              "A Owners",
			ownersFile:            "a",
			expectedLeafApprovers: aApprovers,
			expectedApprovers:     aApprovers.Union(rootApprovers),
		},
		{
			testName:              "B Owners",
			ownersFile:            "b",
			expectedLeafApprovers: bApprovers,
			expectedApprovers:     bApprovers.Union(rootApprovers),
		},
		{
			testName:              "C Owners",
			ownersFile:            "c",
			expectedLeafApprovers: cApprovers,
			expectedApprovers:     cApprovers.Union(rootApprovers),
		},
		{
			testName:              "Combo Owners",
			ownersFile:            "a/combo",
			expectedLeafApprovers: edcApprovers,
			expectedApprovers:     edcApprovers.Union(aApprovers).Union(rootApprovers),
		},
	}

	for _, test := range tests {
		calculatedLeafApprovers := fakeRepo.LeafApprovers(test.ownersFile)
		calculatedApprovers := fakeRepo.Approvers(test.ownersFile)

		test.expectedLeafApprovers = setToLower(test.expectedLeafApprovers)
		if !calculatedLeafApprovers.Equal(test.expectedLeafApprovers) {
			t.Errorf("Failed for test %v.  Expected Leaf Approvers: %v. Actual Leaf Approvers %v", test.testName, test.expectedLeafApprovers, calculatedLeafApprovers)
		}

		test.expectedApprovers = setToLower(test.expectedApprovers)
		if !calculatedApprovers.Set().Equal(test.expectedApprovers) {
			t.Errorf("Failed for test %v.  Expected Approvers: %v. Actual Approvers %v", test.testName, test.expectedApprovers, calculatedApprovers)
		}
	}
}

func TestGetLeafApprovers(t *testing.T) {
	rootApprovers := sets.New[string]("Alice", "Bob")
	aApprovers := sets.New[string]("Art", "Anne")
	bApprovers := sets.New[string]("Bill", "Ben", "Barbara")
	dApprovers := sets.New[string]("David", "Dan", "Debbie")
	FakeRepoMap := map[string]sets.Set[string]{
		"":    rootApprovers,
		"a":   aApprovers,
		"b":   bApprovers,
		"a/d": dApprovers,
	}

	tests := []struct {
		testName    string
		filenames   []string
		expectedMap map[string]sets.Set[string]
	}{
		{
			testName:    "Empty PR",
			filenames:   []string{},
			expectedMap: map[string]sets.Set[string]{},
		},
		{
			testName:    "Single Root File PR",
			filenames:   []string{"kubernetes.go"},
			expectedMap: map[string]sets.Set[string]{"": setToLower(rootApprovers)},
		},
		{
			testName:    "Internal Node File PR",
			filenames:   []string{"a/test.go"},
			expectedMap: map[string]sets.Set[string]{"a": setToLower(aApprovers)},
		},
		{
			testName:  "Two Leaf File PR",
			filenames: []string{"a/d/test.go", "b/test.go"},
			expectedMap: map[string]sets.Set[string]{
				"a/d": setToLower(dApprovers),
				"b":   setToLower(bApprovers)},
		},
		{
			testName:  "Leaf and Parent 2 File PR",
			filenames: []string{"a/test.go", "a/d/test.go"},
			expectedMap: map[string]sets.Set[string]{
				"a": setToLower(aApprovers),
			},
		},
	}

	for _, test := range tests {
		testOwners := Owners{
			filenames: test.filenames,
			repo:      createFakeRepo(FakeRepoMap),
			seed:      TestSeed,
			log:       logrus.WithField("plugin", "some_plugin"),
		}
		oMap := testOwners.GetLeafApprovers()
		if !reflect.DeepEqual(test.expectedMap, oMap) {
			t.Errorf("Failed for test %v.  Expected Owners: %v. Actual Owners %v", test.testName, test.expectedMap, oMap)
		}
	}
}
func TestGetOwnersSet(t *testing.T) {
	rootApprovers := sets.New[string]("Alice", "Bob")
	aApprovers := sets.New[string]("Art", "Anne")
	bApprovers := sets.New[string]("Bill", "Ben", "Barbara")
	dApprovers := sets.New[string]("David", "Dan", "Debbie")
	FakeRepoMap := map[string]sets.Set[string]{
		"":    rootApprovers,
		"a":   aApprovers,
		"b":   bApprovers,
		"a/d": dApprovers,
	}

	tests := []struct {
		testName            string
		filenames           []string
		expectedOwnersFiles sets.Set[string]
	}{
		{
			testName:            "Empty PR",
			filenames:           []string{},
			expectedOwnersFiles: sets.New[string](),
		},
		{
			testName:            "Single Root File PR",
			filenames:           []string{"kubernetes.go"},
			expectedOwnersFiles: sets.New[string](""),
		},
		{
			testName:            "Multiple Root File PR",
			filenames:           []string{"test.go", "kubernetes.go"},
			expectedOwnersFiles: sets.New[string](""),
		},
		{
			testName:            "Internal Node File PR",
			filenames:           []string{"a/test.go"},
			expectedOwnersFiles: sets.New[string]("a"),
		},
		{
			testName:            "Two Leaf File PR",
			filenames:           []string{"a/test.go", "b/test.go"},
			expectedOwnersFiles: sets.New[string]("a", "b"),
		},
		{
			testName:            "Leaf and Parent 2 File PR",
			filenames:           []string{"a/test.go", "a/c/test.go"},
			expectedOwnersFiles: sets.New[string]("a"),
		},
	}

	for _, test := range tests {
		testOwners := Owners{
			filenames: test.filenames,
			repo:      createFakeRepo(FakeRepoMap),
			seed:      TestSeed,
			log:       logrus.WithField("plugin", "some_plugin"),
		}
		oSet := testOwners.GetOwnersSet()
		if !oSet.Equal(test.expectedOwnersFiles) {
			t.Errorf("Failed for test %v.  Expected Owners: %v. Actual Owners %v", test.testName, test.expectedOwnersFiles, oSet)
		}
	}
}

func TestGetSuggestedApprovers(t *testing.T) {
	var rootApprovers = sets.New[string]("Alice", "Bob")
	var aApprovers = sets.New[string]("Art", "Anne")
	var bApprovers = sets.New[string]("Bill", "Ben", "Barbara")
	var dApprovers = sets.New[string]("David", "Dan", "Debbie")
	var eApprovers = sets.New[string]("Eve", "Erin")
	var edcApprovers = eApprovers.Union(dApprovers)
	var FakeRepoMap = map[string]sets.Set[string]{
		"":        rootApprovers,
		"a":       aApprovers,
		"b":       bApprovers,
		"a/d":     dApprovers,
		"a/combo": edcApprovers,
	}
	tests := []struct {
		testName  string
		filenames []string
		// need at least one person from each set
		expectedOwners []sets.Set[string]
	}{
		{
			testName:       "Empty PR",
			filenames:      []string{},
			expectedOwners: []sets.Set[string]{},
		},
		{
			testName:       "Single Root File PR",
			filenames:      []string{"kubernetes.go"},
			expectedOwners: []sets.Set[string]{setToLower(rootApprovers)},
		},
		{
			testName:       "Internal Node File PR",
			filenames:      []string{"a/test.go"},
			expectedOwners: []sets.Set[string]{setToLower(aApprovers)},
		},
		{
			testName:       "Multiple Files Internal Node File PR",
			filenames:      []string{"a/test.go", "a/test1.go"},
			expectedOwners: []sets.Set[string]{setToLower(aApprovers)},
		},
		{
			testName:       "Two Leaf File PR",
			filenames:      []string{"a/test.go", "b/test.go"},
			expectedOwners: []sets.Set[string]{setToLower(aApprovers), setToLower(bApprovers)},
		},
		{
			testName:       "Leaf and Parent 2 File PR",
			filenames:      []string{"a/test.go", "a/d/test.go"},
			expectedOwners: []sets.Set[string]{setToLower(aApprovers)},
		},
		{
			testName:       "Combo and B",
			filenames:      []string{"a/combo/test.go", "b/test.go"},
			expectedOwners: []sets.Set[string]{setToLower(edcApprovers), setToLower(bApprovers)},
		},
		{
			testName:       "Lowest Leaf",
			filenames:      []string{"a/combo/test.go"},
			expectedOwners: []sets.Set[string]{setToLower(edcApprovers)},
		},
	}

	for _, test := range tests {
		testOwners := Owners{
			filenames: test.filenames,
			repo:      createFakeRepo(FakeRepoMap),
			seed:      TestSeed,
			log:       logrus.WithField("plugin", "some_plugin"),
		}
		suggested := testOwners.GetSuggestedApprovers(testOwners.GetReverseMap(testOwners.GetLeafApprovers()), testOwners.GetShuffledApprovers())
		for _, ownersSet := range test.expectedOwners {
			if ownersSet.Intersection(suggested).Len() == 0 {
				t.Errorf("Failed for test %v.  Didn't find an approver from: %v. Actual Owners %v", test.testName, ownersSet, suggested)
				t.Errorf("%v", test.filenames)
			}
		}
	}
}

func TestGetAllPotentialApprovers(t *testing.T) {
	rootApprovers := sets.New[string]("Alice", "Bob")
	aApprovers := sets.New[string]("Art", "Anne")
	bApprovers := sets.New[string]("Bill", "Ben", "Barbara")
	cApprovers := sets.New[string]("Chris", "Carol")
	dApprovers := sets.New[string]("David", "Dan", "Debbie")
	eApprovers := sets.New[string]("Eve", "Erin")
	edcApprovers := eApprovers.Union(dApprovers).Union(cApprovers)
	FakeRepoMap := map[string]sets.Set[string]{
		"":        rootApprovers,
		"a":       aApprovers,
		"b":       bApprovers,
		"c":       cApprovers,
		"a/d":     dApprovers,
		"a/combo": edcApprovers,
	}
	tests := []struct {
		testName  string
		filenames []string
		// use an array because we expected output of this function to be sorted
		expectedApprovers []string
	}{
		{
			testName:          "Empty PR",
			filenames:         []string{},
			expectedApprovers: []string{},
		},
		{
			testName:          "Single Root File PR",
			filenames:         []string{"kubernetes.go"},
			expectedApprovers: sets.List(setToLower(rootApprovers)),
		},
		{
			testName:          "Internal Node File PR",
			filenames:         []string{"a/test.go"},
			expectedApprovers: sets.List(setToLower(aApprovers)),
		},
		{
			testName:          "One Leaf One Internal Node File PR",
			filenames:         []string{"a/test.go", "b/test.go"},
			expectedApprovers: sets.List(setToLower(aApprovers.Union(bApprovers))),
		},
		{
			testName:          "Two Leaf Files PR",
			filenames:         []string{"a/d/test.go", "c/test.go"},
			expectedApprovers: sets.List(setToLower(dApprovers.Union(cApprovers))),
		},
		{
			testName:          "Leaf and Parent 2 File PR",
			filenames:         []string{"a/test.go", "a/combo/test.go"},
			expectedApprovers: sets.List(setToLower(aApprovers)),
		},
		{
			testName:          "Two Leafs",
			filenames:         []string{"a/d/test.go", "b/test.go"},
			expectedApprovers: sets.List(setToLower(dApprovers.Union(bApprovers))),
		},
		{
			testName:          "Lowest Leaf",
			filenames:         []string{"a/combo/test.go"},
			expectedApprovers: sets.List(setToLower(edcApprovers)),
		},
		{
			testName:          "Root And Everything Else PR",
			filenames:         []string{"a/combo/test.go", "b/test.go", "c/test.go", "d/test.go"},
			expectedApprovers: sets.List(setToLower(rootApprovers)),
		},
	}

	for _, test := range tests {
		testOwners := Owners{
			filenames: test.filenames,
			repo:      createFakeRepo(FakeRepoMap),
			seed:      TestSeed,
			log:       logrus.WithField("plugin", "some_plugin"),
		}
		all := testOwners.GetAllPotentialApprovers()
		if !reflect.DeepEqual(all, test.expectedApprovers) {
			t.Errorf("Failed for test %v.  Didn't correct approvers list.  Expected: %v. Found %v", test.testName, test.expectedApprovers, all)
		}
	}
}

func TestFindMostCoveringApprover(t *testing.T) {
	rootApprovers := sets.New[string]("Alice", "Bob")
	aApprovers := sets.New[string]("Art", "Anne")
	bApprovers := sets.New[string]("Bill", "Ben", "Barbara")
	cApprovers := sets.New[string]("Chris", "Carol")
	dApprovers := sets.New[string]("David", "Dan", "Debbie")
	eApprovers := sets.New[string]("Eve", "Erin")
	edcApprovers := eApprovers.Union(dApprovers).Union(cApprovers)
	FakeRepoMap := map[string]sets.Set[string]{
		"":        rootApprovers,
		"a":       aApprovers,
		"b":       bApprovers,
		"c":       cApprovers,
		"a/d":     dApprovers,
		"a/combo": edcApprovers,
	}
	tests := []struct {
		testName   string
		filenames  []string
		unapproved sets.Set[string]
		// because most covering could be two or more people
		expectedMostCovering sets.Set[string]
	}{
		{
			testName:             "Empty PR",
			filenames:            []string{},
			unapproved:           sets.Set[string]{},
			expectedMostCovering: sets.New[string](""),
		},
		{
			testName:             "Single Root File PR",
			filenames:            []string{"kubernetes.go"},
			unapproved:           sets.New[string](""),
			expectedMostCovering: setToLower(rootApprovers),
		},
		{
			testName:             "Internal Node File PR",
			filenames:            []string{"a/test.go"},
			unapproved:           sets.New[string]("a"),
			expectedMostCovering: setToLower(aApprovers),
		},
		{
			testName:             "Combo and Intersecting Leaf PR",
			filenames:            []string{"a/combo/test.go", "a/d/test.go"},
			unapproved:           sets.New[string]("a/combo", "a/d"),
			expectedMostCovering: setToLower(edcApprovers.Intersection(dApprovers)),
		},
		{
			testName:             "Three Leaf PR Only B Approved",
			filenames:            []string{"a/combo/test.go", "c/test.go", "b/test.go"},
			unapproved:           sets.New[string]("a/combo", "c/"),
			expectedMostCovering: setToLower(edcApprovers.Intersection(cApprovers)),
		},
		{
			testName:             "Three Leaf PR Only B Left Unapproved",
			filenames:            []string{"a/combo/test.go", "a/d/test.go", "b/test.go"},
			unapproved:           sets.New[string]("b"),
			expectedMostCovering: setToLower(bApprovers),
		},
		{
			testName:             "Leaf and Parent 2 File PR",
			filenames:            []string{"a/test.go", "a/d/test.go"},
			unapproved:           sets.New[string]("a", "a/d"),
			expectedMostCovering: setToLower(aApprovers.Union(dApprovers)),
		},
	}

	for _, test := range tests {
		testOwners := Owners{
			filenames: test.filenames,
			repo:      createFakeRepo(FakeRepoMap),
			seed:      TestSeed,
			log:       logrus.WithField("plugin", "some_plugin"),
		}
		bestPerson := findMostCoveringApprover(testOwners.GetAllPotentialApprovers(), nil, testOwners.GetReverseMap(testOwners.GetLeafApprovers()), test.unapproved)
		if test.expectedMostCovering.Intersection(sets.New[string](bestPerson)).Len() != 1 {
			t.Errorf("Failed for test %v.  Didn't correct approvers list.  Expected: %v. Found %v", test.testName, test.expectedMostCovering, bestPerson)
		}

	}
}

func TestGetReverseMap(t *testing.T) {
	rootApprovers := sets.New[string]("Alice", "Bob")
	aApprovers := sets.New[string]("Art", "Anne")
	cApprovers := sets.New[string]("Chris", "Carol")
	dApprovers := sets.New[string]("David", "Dan", "Debbie")
	eApprovers := sets.New[string]("Eve", "Erin")
	edcApprovers := eApprovers.Union(dApprovers).Union(cApprovers)
	FakeRepoMap := map[string]sets.Set[string]{
		"":        rootApprovers,
		"a":       aApprovers,
		"c":       cApprovers,
		"a/d":     dApprovers,
		"a/combo": edcApprovers,
	}
	tests := []struct {
		testName       string
		filenames      []string
		expectedRevMap map[string]sets.Set[string] // people -> files they can approve
	}{
		{
			testName:       "Empty PR",
			filenames:      []string{},
			expectedRevMap: map[string]sets.Set[string]{},
		},
		{
			testName:  "Single Root File PR",
			filenames: []string{"kubernetes.go"},
			expectedRevMap: map[string]sets.Set[string]{
				"alice": sets.New[string](""),
				"bob":   sets.New[string](""),
			},
		},
		{
			testName:  "Two Leaf PRs",
			filenames: []string{"a/combo/test.go", "a/d/test.go"},
			expectedRevMap: map[string]sets.Set[string]{
				"david":  sets.New[string]("a/d", "a/combo"),
				"dan":    sets.New[string]("a/d", "a/combo"),
				"debbie": sets.New[string]("a/d", "a/combo"),
				"eve":    sets.New[string]("a/combo"),
				"erin":   sets.New[string]("a/combo"),
				"chris":  sets.New[string]("a/combo"),
				"carol":  sets.New[string]("a/combo"),
			},
		},
	}

	for _, test := range tests {
		testOwners := Owners{
			filenames: test.filenames,
			repo:      createFakeRepo(FakeRepoMap),
			seed:      TestSeed,
			log:       logrus.WithField("plugin", "some_plugin"),
		}
		calculatedRevMap := testOwners.GetReverseMap(testOwners.GetLeafApprovers())
		if !reflect.DeepEqual(calculatedRevMap, test.expectedRevMap) {
			t.Errorf("Failed for test %v.  Didn't find correct reverse map.", test.testName)
			t.Errorf("Person \t\t Expected \t\tFound ")
			// printing the calculated vs expected in a nicer way for debugging
			for k, v := range test.expectedRevMap {
				if calcVal, ok := calculatedRevMap[k]; ok {
					t.Errorf("%v\t\t%v\t\t%v ", k, v, calcVal)
				} else {
					t.Errorf("%v\t\t%v", k, v)
				}
			}
		}
	}
}

func TestGetShuffledApprovers(t *testing.T) {
	rootApprovers := sets.New[string]("Alice", "Bob")
	aApprovers := sets.New[string]("Art", "Anne")
	bApprovers := sets.New[string]("Bill", "Ben", "Barbara")
	cApprovers := sets.New[string]("Chris", "Carol")
	dApprovers := sets.New[string]("David", "Dan", "Debbie")
	eApprovers := sets.New[string]("Eve", "Erin")
	edcApprovers := eApprovers.Union(dApprovers).Union(cApprovers)
	FakeRepoMap := map[string]sets.Set[string]{
		"":        rootApprovers,
		"a":       aApprovers,
		"b":       bApprovers,
		"c":       cApprovers,
		"a/d":     dApprovers,
		"a/combo": edcApprovers,
	}
	tests := []struct {
		testName      string
		filenames     []string
		seed          int64
		expectedOrder []string
	}{
		{
			testName:      "Empty PR",
			filenames:     []string{},
			seed:          0,
			expectedOrder: []string{},
		},
		{
			testName:      "Single Root File PR Approved",
			filenames:     []string{"kubernetes.go"},
			seed:          0,
			expectedOrder: []string{"bob", "alice"},
		},
		{
			testName:      "Combo And B PR",
			filenames:     []string{"a/combo/test.go", "b/test.go"},
			seed:          0,
			expectedOrder: []string{"erin", "bill", "carol", "barbara", "dan", "debbie", "ben", "david", "eve", "chris"},
		},
		{
			testName:      "Combo and D, Seed 0",
			filenames:     []string{"a/combo/test.go", "a/d/test.go"},
			seed:          0,
			expectedOrder: []string{"erin", "dan", "dan", "carol", "david", "debbie", "chris", "debbie", "eve", "david"},
		},
		{
			testName:      "Combo and D, Seed 2",
			filenames:     []string{"a/combo/test.go", "a/d/test.go"},
			seed:          2,
			expectedOrder: []string{"dan", "carol", "debbie", "dan", "erin", "chris", "eve", "david", "debbie", "david"},
		},
	}

	for _, test := range tests {
		testOwners := Owners{
			filenames: test.filenames,
			repo:      createFakeRepo(FakeRepoMap),
			seed:      test.seed,
			log:       logrus.WithField("plugin", "some_plugin"),
		}
		calculated := testOwners.GetShuffledApprovers()
		if !reflect.DeepEqual(test.expectedOrder, calculated) {
			t.Errorf("Failed for test %v.  Expected unapproved files: %v. Found %v", test.testName, test.expectedOrder, calculated)
		}
	}
}

func TestRemoveSubdirs(t *testing.T) {
	tests := []struct {
		testName       string
		directories    sets.Set[string]
		noParentOwners map[string]bool

		expected sets.Set[string]
	}{
		{
			testName:    "Empty PR",
			directories: sets.New[string](),
			expected:    sets.New[string](),
		},
		{
			testName:    "Root and One Level Below PR",
			directories: sets.New[string]("", "a/"),
			expected:    sets.New[string](""),
		},
		{
			testName:    "Two Separate Branches",
			directories: sets.New[string]("a/", "c/"),
			expected:    sets.New[string]("a/", "c/"),
		},
		{
			testName:    "Lots of Branches and Leaves",
			directories: sets.New[string]("a", "a/combo", "a/d", "b", "c"),
			expected:    sets.New[string]("a", "b", "c"),
		},
		{
			testName:       "NoParentOwners",
			directories:    sets.New[string]("a", "a/combo"),
			noParentOwners: map[string]bool{"a/combo": true},
			expected:       sets.New[string]("a", "a/combo"),
		},
		{
			testName:       "NoParentOwners in relative path",
			directories:    sets.New[string]("a", "a/b/combo"),
			noParentOwners: map[string]bool{"a/b": true},
			expected:       sets.New[string]("a", "a/b/combo"),
		},
		{
			testName:       "NoParentOwners with child",
			directories:    sets.New[string]("a", "a/b", "a/b/combo"),
			noParentOwners: map[string]bool{"a/b": true},
			expected:       sets.New[string]("a", "a/b"),
		},
	}

	for _, test := range tests {
		if test.noParentOwners == nil {
			test.noParentOwners = map[string]bool{}
		}
		o := &Owners{repo: FakeRepo{noParentOwnersMap: test.noParentOwners}}
		o.removeSubdirs(test.directories)
		if !reflect.DeepEqual(test.expected, test.directories) {
			t.Errorf("Failed to remove subdirectories for test %v.  Expected files: %q. Found %q", test.testName, sets.List(test.expected), sets.List(test.directories))

		}
	}
}
