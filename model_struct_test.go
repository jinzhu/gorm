package gorm_test

import (
	"sync"
	"testing"

	"github.com/jinzhu/gorm"
)

type ModelA struct {
	gorm.Model
	Name string

	ModelCs []ModelC `gorm:"foreignkey:OtherAID"`
}

type ModelB struct {
	gorm.Model
	Name string

	ModelCs []ModelC `gorm:"foreignkey:OtherBID"`
}

type ModelC struct {
	gorm.Model
	Name string

	OtherAID uint64
	OtherA   *ModelA `gorm:"foreignkey:OtherAID"`
	OtherBID uint64
	OtherB   *ModelB `gorm:"foreignkey:OtherBID"`
}

type RequestModel struct {
	Name     string
	Children []ChildModel `gorm:"foreignkey:ParentID"`
}

type ChildModel struct {
	ID       string
	ParentID string
	Name     string
}

type ResponseModel struct {
	gorm.Model
	RequestModel
}

// This test will try to cause a race condition on the model's foreignkey metadata
func TestModelStructRaceSameModel(t *testing.T) {
	// use a WaitGroup to execute as much in-sync as possible
	// it's more likely to hit a race condition than without
	n := 32
	start := sync.WaitGroup{}
	start.Add(n)

	// use another WaitGroup to know when the test is done
	done := sync.WaitGroup{}
	done.Add(n)

	for i := 0; i < n; i++ {
		go func() {
			start.Wait()

			// call GetStructFields, this had a race condition before we fixed it
			DB.NewScope(&ModelA{}).GetStructFields()

			done.Done()
		}()

		start.Done()
	}

	done.Wait()
}

// This test will try to cause a race condition on the model's foreignkey metadata
func TestModelStructRaceDifferentModel(t *testing.T) {
	// use a WaitGroup to execute as much in-sync as possible
	// it's more likely to hit a race condition than without
	n := 32
	start := sync.WaitGroup{}
	start.Add(n)

	// use another WaitGroup to know when the test is done
	done := sync.WaitGroup{}
	done.Add(n)

	for i := 0; i < n; i++ {
		i := i
		go func() {
			start.Wait()

			// call GetStructFields, this had a race condition before we fixed it
			if i%2 == 0 {
				DB.NewScope(&ModelA{}).GetStructFields()
			} else {
				DB.NewScope(&ModelB{}).GetStructFields()
			}

			done.Done()
		}()

		start.Done()
	}

	done.Wait()
}

func TestModelStructEmbeddedHasMany(t *testing.T) {
	fields := DB.NewScope(&ResponseModel{}).GetStructFields()

	var childrenField *gorm.StructField

	for i := 0; i < len(fields); i++ {
		field := fields[i]

		if field != nil && field.Name == "Children" {
			childrenField = field
		}
	}

	if childrenField == nil {
		t.Error("childrenField should not be nil")
		return
	}

	if childrenField.Relationship == nil {
		t.Error("childrenField.Relation should not be nil")
		return
	}

	expected := "has_many"
	actual := childrenField.Relationship.Kind

	if actual != expected {
		t.Errorf("childrenField.Relationship.Kind should be %v, but was %v", expected, actual)
	}
}
