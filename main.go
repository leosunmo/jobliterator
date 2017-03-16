package main

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"time"

	"github.com/ericchiang/k8s"
	"github.com/ghodss/yaml"
)

type kubeJob struct {
	name      string
	namespace string
	age       int
}

func loadClient(kubeconfigPath, kubeContext string) (*k8s.Client, error) {
	data, err := ioutil.ReadFile(kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("read kubeconfig: %v", err)
	}

	// Unmarshal YAML into a Kubernetes config object.
	var config k8s.Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("unmarshal kubeconfig: %v", err)
	}
	if kubeContext != "" {
		config.CurrentContext = kubeContext
	}
	return k8s.NewClient(&config)
}

func main() {
	kubeconfigPath := flag.String("kubeconfig", "./config", "path to the kubeconfig file")
	kubeContext := flag.String("context", "", "override current-context (default 'current-context' in kubeconfig)")
	kubeNamespace := flag.String("namespace", "", "specific namespace (default all namespaces)")
	deleteJobs := flag.Bool("f", false, "force delete the jobs (default simulate without deleting)")
	olderThanDays := flag.Int("days", 7, "set delete threshold in days")
	flag.Parse()
	//uses the current context in kubeconfig unless overriden using '-context'
	client, err := loadClient(*kubeconfigPath, *kubeContext)
	if err != nil {
		fmt.Printf("Failed to read kubeconfig (%s). Exiting.\n", *kubeconfigPath)
		os.Exit(1)
	}

	// Retrive a list of all jobs in the current context and namespace
	jobs, err := client.BatchV1().ListJobs(context.Background(), *kubeNamespace)
	if err != nil {
		panic(err.Error())
	}

	now := time.Now()
	var eligibleJobs []kubeJob
	for _, j := range jobs.Items {
		if *j.Status.Active == 1 {
			continue
		}
		completionTime := time.Unix(j.Status.GetCompletionTime().GetSeconds(), 0)
		daysOld := int(now.Sub(completionTime).Hours() / 24)
		if daysOld >= *olderThanDays {
			eligibleJobs = append(eligibleJobs, kubeJob{name: *j.Metadata.Name, namespace: *j.Metadata.Namespace, age: daysOld})
		}
	}

	if *deleteJobs {
		for _, dj := range eligibleJobs {
			fmt.Printf("Deleting job: %s\tAge:%vd\n", dj.name, dj.age)
			err := client.BatchV1().DeleteJob(context.Background(), dj.name, dj.namespace)
			if err != nil {
				fmt.Println("Unable to delete job %s.\n Error: %v\n", dj.name, err.Error())
				continue
			}
		}
	} else {
		fmt.Println("Jobs eligible for deletion with -f flag:")
		for _, dj := range eligibleJobs {
			fmt.Printf("Name: %s\t\tAge:%vd\n", dj.name, dj.age)
		}
	}
	fmt.Printf("Total: %v\n", len(eligibleJobs))
}
