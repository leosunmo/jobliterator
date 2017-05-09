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
type kubePod struct {
	name      string
	namespace string
	phase     string
}

func loadClient(kubeconfigPath, kubeContext string, inCluster bool) (*k8s.Client, error) {
	if inCluster {
		client, err := k8s.NewInClusterClient()
		if err != nil {
			return nil, fmt.Errorf("Failed to create in-cluster client: %v", err)
		}
		return client, nil
	}
	data, err := ioutil.ReadFile(kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("Failed to read kubeconfig: %v", err)
	}

	// Unmarshal YAML into a Kubernetes config object.
	var config k8s.Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("Failed to unmarshal kubeconfig: %v", err)
	}
	if kubeContext != "" {
		config.CurrentContext = kubeContext
	}
	return k8s.NewClient(&config)
}

func main() {
	kubeconfigPath := flag.String("kubeconfig", "./config", "path to the kubeconfig file")
	inCluster := flag.Bool("in-cluster", false, "Use in-cluster credentials")
	kubeContext := flag.String("context", "", "override current-context (default 'current-context' in kubeconfig)")
	kubeNamespace := flag.String("namespace", "", "specific namespace (default all namespaces)")
	deleteJobs := flag.Bool("f", false, "force delete the jobs (default simulate without deleting)")
	olderThanDays := flag.Int("days", 7, "set delete threshold in days")
	flag.Parse()
	//uses the current context in kubeconfig unless overriden using '-context'
	client, err := loadClient(*kubeconfigPath, *kubeContext, *inCluster)
	if err != nil {
		fmt.Println(err.Error())
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
			// First use the job label to find the corresponding pods to delete
			podLS := new(k8s.LabelSelector)
			podLS.Eq("job-name", dj.name)
			pods, podErr := client.CoreV1().ListPods(context.Background(), *kubeNamespace, podLS.Selector())
			if podErr != nil {
				fmt.Printf("Unable to list jobs with label job-name=%s. Skipping this job.", dj.name)
				fmt.Printf("ERROR: Job %s skipped. %s.", dj.name, podErr.Error())
				continue
			}
			var eligiblePods []kubePod
			for _, p := range pods.Items {

				// Build a slice of eligible jobs to avoid calling the API more than needed
				if *p.Status.Phase == "Succeeded" || *p.Status.Phase == "Failed" {
					eligiblePods = append(eligiblePods, kubePod{name: p.Metadata.GetName(), namespace: p.Metadata.GetNamespace(), phase: p.Status.GetPhase()})
				} else {
					fmt.Printf("\tPod associated with %s is not in \"Succeeded\" or \"Failed\" phase but job is complete.", dj.name)
					fmt.Printf("\tPod %s is in phase %s, skipping.", p.Metadata.GetName(), p.Status.GetPhase())
				}
			}
			if len(eligiblePods) > 0 {
				for _, dp := range eligiblePods {
					fmt.Printf("\tDeleting pod: %s\tPhase: %s\n", dp.name, dp.phase)
					podErr = client.CoreV1().DeletePod(context.Background(), dp.name, dp.namespace)
					if podErr != nil {
						fmt.Printf("\tUnable to delete pod %s. Error: %s\n", dp.name, podErr.Error())
						continue
					}
				}
			} else {
				fmt.Printf("\tNo pods eligible for deletion associated with job %s.", dj.name)
			}

			err2 := client.BatchV1().DeleteJob(context.Background(), dj.name, dj.namespace)
			if err2 != nil {
				fmt.Println("Unable to delete job %s.\n Error: %v\n", dj.name, err2.Error())
				continue
			}
		}
	} else {
		fmt.Println("Jobs eligible for deletion with -f flag:")
		for _, dj := range eligibleJobs {
			fmt.Printf("Name: %s\t\tAge:%vd\n", dj.name, dj.age)
			podLS := new(k8s.LabelSelector)
			podLS.Eq("job-name", dj.name)
			pods, podErr := client.CoreV1().ListPods(context.Background(), *kubeNamespace, podLS.Selector())
			if podErr != nil {
				fmt.Printf("Unable to list jobs with label job-name=%s. Skipping this job.", dj.name)
				fmt.Printf("ERROR: Job %s skipped. %s.", dj.name, podErr.Error())
				continue
			}
			var eligiblePods []kubePod
			for _, p := range pods.Items {
				if *p.Status.Phase == "Succeeded" || *p.Status.Phase == "Failed" {
					eligiblePods = append(eligiblePods, kubePod{name: p.Metadata.GetName(), namespace: p.Metadata.GetNamespace(), phase: p.Status.GetPhase()})
				} else {
					fmt.Printf("\tPod associated with %s is not in \"Succeeded\" or \"Failed\" phase but job is complete.", dj.name)
					fmt.Printf("\tPod %s is in phase %s, skipping.", p.Metadata.GetName(), p.Status.GetPhase())
				}
			}
			if len(eligiblePods) > 0 {
				for _, dp := range eligiblePods {
					fmt.Printf("\tPod: %s\t\tPhase: %s\n", dp.name, dp.phase)
				}
			} else {
				fmt.Printf("\tNo pods eligible for deletion associated with job %s.", dj.name)
			}
		}
	}
	fmt.Printf("Total Jobs: %v\n", len(eligibleJobs))
	fmt.Println("Searching for orphaned pods...")
	pods, podErr := client.CoreV1().ListPods(context.Background(), *kubeNamespace)
	if podErr != nil {
		fmt.Printf("ERROR: %s.", podErr.Error())
	} else {
		if len(pods.Items) > 0 {
			for _, pod := range pods.Items {
				pl := pod.Metadata.GetLabels()
				if val, ok := pl["job-name"]; ok {
					jobCheck, err := client.BatchV1().GetJob(context.Background(), val, *kubeNamespace)
					if err != nil {
						if apiErr, ok := err.(*k8s.APIError); ok {
							if apiErr.Code == 404 {
								jobCheck = nil
							}
						} else {
							fmt.Printf("Error getting job: %s", err.Error())
						}
					}
					if jobCheck == nil {
						fmt.Printf("Job: %s", val)
						fmt.Printf("\tPod: %s\tNamespace: %s\tPhase: %s\n", pod.Metadata.GetName(),,pod.Metadata.GetNamespace(), pod.Status.GetPhase())
					}
				}
			}
		} else {
			fmt.Println("Unable to find any pods.")
		}
	}
}
