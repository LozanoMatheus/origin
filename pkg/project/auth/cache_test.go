package auth

import (
	"fmt"
	"strconv"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apiserver/pkg/authentication/user"
	kapi "k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/client/clientset_generated/internalclientset/fake"
	informers "k8s.io/kubernetes/pkg/client/informers/informers_generated/internalversion"
	"k8s.io/kubernetes/pkg/controller"

	authorizationapi "github.com/openshift/origin/pkg/authorization/apis/authorization"
	authorizationlister "github.com/openshift/origin/pkg/authorization/generated/listers/authorization/internalversion"
)

type MockPolicyGetter struct{}

func (m MockPolicyGetter) Policies(namespace string) authorizationlister.PolicyNamespaceLister {
	return m
}
func (m MockPolicyGetter) List(labels.Selector) ([]*authorizationapi.Policy, error) {
	return nil, nil
}
func (m MockPolicyGetter) Get(name string) (*authorizationapi.Policy, error) {
	return &authorizationapi.Policy{}, nil
}
func (m MockPolicyGetter) LastSyncResourceVersion() string { return "" }

type MockClusterPolicyGetter struct{}

func (m MockClusterPolicyGetter) List(labels.Selector) ([]*authorizationapi.ClusterPolicy, error) {
	return nil, nil
}
func (m MockClusterPolicyGetter) Get(name string) (*authorizationapi.ClusterPolicy, error) {
	return &authorizationapi.ClusterPolicy{}, nil
}
func (m MockClusterPolicyGetter) LastSyncResourceVersion() string { return "" }

type MockPolicyBindingGetter struct{}

func (m MockPolicyBindingGetter) PolicyBindings(namespace string) authorizationlister.PolicyBindingNamespaceLister {
	return m
}
func (m MockPolicyBindingGetter) List(labels.Selector) ([]*authorizationapi.PolicyBinding, error) {
	return nil, nil
}
func (m MockPolicyBindingGetter) Get(name string) (*authorizationapi.PolicyBinding, error) {
	return &authorizationapi.PolicyBinding{}, nil
}
func (m MockPolicyBindingGetter) LastSyncResourceVersion() string { return "" }

type MockClusterPolicyBindingGetter struct{}

func (m MockClusterPolicyBindingGetter) List(labels.Selector) ([]*authorizationapi.ClusterPolicyBinding, error) {
	return nil, nil
}
func (m MockClusterPolicyBindingGetter) Get(name string) (*authorizationapi.ClusterPolicyBinding, error) {
	return &authorizationapi.ClusterPolicyBinding{}, nil
}
func (m MockClusterPolicyBindingGetter) LastSyncResourceVersion() string { return "" }

// mockReview implements the Review interface for test cases
type mockReview struct {
	users  []string
	groups []string
	err    string
}

// Users returns the users that can access a resource
func (r *mockReview) Users() []string {
	return r.users
}

// Groups returns the groups that can access a resource
func (r *mockReview) Groups() []string {
	return r.groups
}

func (r *mockReview) EvaluationError() string {
	return r.err
}

// common test users
var (
	alice = &user.DefaultInfo{
		Name:   "Alice",
		UID:    "alice-uid",
		Groups: []string{},
	}
	bob = &user.DefaultInfo{
		Name:   "Bob",
		UID:    "bob-uid",
		Groups: []string{"employee"},
	}
	eve = &user.DefaultInfo{
		Name:   "Eve",
		UID:    "eve-uid",
		Groups: []string{"employee"},
	}
	frank = &user.DefaultInfo{
		Name:   "Frank",
		UID:    "frank-uid",
		Groups: []string{},
	}
)

// mockReviewer returns the specified values for each supplied resource
type mockReviewer struct {
	expectedResults map[string]*mockReview
}

// Review returns the mapped review from the mock object, or an error if none exists
func (mr *mockReviewer) Review(name string) (Review, error) {
	review := mr.expectedResults[name]
	if review == nil {
		return nil, fmt.Errorf("Item %s does not exist", name)
	}
	return review, nil
}

func validateList(t *testing.T, lister Lister, user user.Info, expectedSet sets.String) {
	namespaceList, err := lister.List(user)
	if err != nil {
		t.Errorf("Unexpected error %v", err)
	}
	results := sets.String{}
	for _, namespace := range namespaceList.Items {
		results.Insert(namespace.Name)
	}
	if results.Len() != expectedSet.Len() || !results.HasAll(expectedSet.List()...) {
		t.Errorf("User %v, Expected: %v, Actual: %v", user.GetName(), expectedSet, results)
	}
}

func TestSyncNamespace(t *testing.T) {
	namespaceList := kapi.NamespaceList{
		Items: []kapi.Namespace{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "foo", ResourceVersion: "1"},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "bar", ResourceVersion: "2"},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "car", ResourceVersion: "3"},
			},
		},
	}
	mockKubeClient := fake.NewSimpleClientset(&namespaceList)

	reviewer := &mockReviewer{
		expectedResults: map[string]*mockReview{
			"foo": {
				users:  []string{alice.GetName(), bob.GetName()},
				groups: eve.GetGroups(),
			},
			"bar": {
				users:  []string{frank.GetName(), eve.GetName()},
				groups: []string{"random"},
			},
			"car": {
				users:  []string{},
				groups: []string{},
			},
		},
	}

	informers := informers.NewSharedInformerFactory(mockKubeClient, controller.NoResyncPeriodFunc())

	authorizationCache := NewAuthorizationCache(
		informers.Core().InternalVersion().Namespaces().Informer(),
		reviewer,
		MockClusterPolicyGetter{}, MockClusterPolicyBindingGetter{},
		MockPolicyGetter{}, MockPolicyBindingGetter{},
	)
	// we prime the data we need here since we are not running reflectors
	for i := range namespaceList.Items {
		authorizationCache.namespaceStore.Add(&namespaceList.Items[i])
	}

	// synchronize the cache
	authorizationCache.synchronize()

	validateList(t, authorizationCache, alice, sets.NewString("foo"))
	validateList(t, authorizationCache, bob, sets.NewString("foo"))
	validateList(t, authorizationCache, eve, sets.NewString("foo", "bar"))
	validateList(t, authorizationCache, frank, sets.NewString("bar"))

	// modify access rules
	reviewer.expectedResults["foo"].users = []string{bob.GetName()}
	reviewer.expectedResults["foo"].groups = []string{"random"}
	reviewer.expectedResults["bar"].users = []string{alice.GetName(), eve.GetName()}
	reviewer.expectedResults["bar"].groups = []string{"employee"}
	reviewer.expectedResults["car"].users = []string{bob.GetName(), eve.GetName()}
	reviewer.expectedResults["car"].groups = []string{"employee"}

	// modify resource version on each namespace to simulate a change had occurred to force cache refresh
	for i := range namespaceList.Items {
		namespace := namespaceList.Items[i]
		oldVersion, err := strconv.Atoi(namespace.ResourceVersion)
		if err != nil {
			t.Errorf("Bad test setup, resource versions should be numbered, %v", err)
		}
		newVersion := strconv.Itoa(oldVersion + 1)
		namespace.ResourceVersion = newVersion
		authorizationCache.namespaceStore.Add(&namespace)
	}

	// now refresh the cache (which is resource version aware)
	authorizationCache.synchronize()

	// make sure new rights hold
	validateList(t, authorizationCache, alice, sets.NewString("bar"))
	validateList(t, authorizationCache, bob, sets.NewString("foo", "bar", "car"))
	validateList(t, authorizationCache, eve, sets.NewString("bar", "car"))
	validateList(t, authorizationCache, frank, sets.NewString())
}
