package main

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"time"

	"github.com/ericchiang/k8s"
	"github.com/ghodss/yaml"
)

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
		panic(err.Error)
	}

	// Retrive a list of all jobs in the current context and namespace
	jobs, err := client.BatchV1().ListJobs(context.Background(), *kubeNamespace)
	if err != nil {
		panic(err.Error())
	}
	now := time.Now()
	eligibleJobs := []string{}
	for _, j := range jobs.Items {
		completionTime := time.Unix(j.Status.GetCompletionTime().GetSeconds(), 0)
		daysOld := int(now.Sub(completionTime).Hours() / 24)
		if daysOld >= *olderThanDays {
			eligibleJobs = append(eligibleJobs, *j.Metadata.Name)
		}
	}

	if *deleteJobs {
		for _, dj := range eligibleJobs {
			fmt.Printf("Deleting job: %s\n", dj)
			client.BatchV1().DeleteJob(context.Background(), dj, *kubeNamespace)
		}
	} else {
		fmt.Println("Jobs eligible for deletion with -f flag:")
		for _, dj := range eligibleJobs {
			fmt.Println(dj)
		}
	}
	fmt.Printf("Total: %v\n", len(eligibleJobs))
}
