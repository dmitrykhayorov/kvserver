package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"io"
	"kvstorage/service/internal/models"
	"kvstorage/service/internal/storage"
	"log"
	"math/rand"
	"net"
	"net/http"
	"time"
)

type Service struct {
	storage    *storage.KVStore
	ln         net.Listener
	Id         string
	Addr       string
	ReplFact   int
	Cluster    []string
	IsLeader   bool
	LeaderAddr string
}

const (
	HeartbeatInterval = 5 * time.Second
)

var (
	lastHeartbeat time.Time
	leaderTimeout time.Duration
)

type Op struct {
	Op   string
	Data models.Data
}

func NewService(id, addr string, replFact int) *Service {
	return &Service{
		storage:  storage.NewKVStore(),
		Id:       id,
		Addr:     addr,
		ReplFact: replFact,
		Cluster:  []string{addr},
		IsLeader: false,
	}
}

func (s *Service) Start(joinAddr string) error {
	server := http.Server{
		Handler: s,
	}
	ln, err := net.Listen("tcp", s.Addr)

	if err != nil {
		DPrintf("[%s] cannot start service: %s", s.Id, err)
		return err
	}
	DPrintf("[%s] created listener for address: %s", s.Id, s.Addr)
	s.ln = ln

	http.Handle("/", s)

	go func() {
		err := server.Serve(s.ln)
		if err != nil {
			log.Fatalf("HTTP serve:%s", err)
		}

	}()

	if joinAddr != "" {
		err = s.JoinCluster(joinAddr)
		if err != nil {
			DPrintf("[%s] cannot join cluster with addr : %s", s.Id, joinAddr)
		}
	}
	source := rand.NewSource(time.Now().Unix())
	rd := rand.New(source)
	leaderTimeout = time.Duration(rd.Intn(10))*time.Second + HeartbeatInterval

	go func() {
		for {
			if s.IsLeader {
				s.SendHeartbeat()
			} else {
				s.CheckLeader()
			}
			time.Sleep(HeartbeatInterval)
		}
	}()

	DPrintf("[%s] exited start serve for: %s", s.Id, s.Addr)
	return nil
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/get" && r.Method == "GET" {
		s.GetHandler(w, r)
	} else if r.URL.Path == "/set" && r.Method == "POST" {
		s.SetHandler(w, r)
	} else if r.URL.Path == "/delete" && r.Method == "DELETE" {
		s.DeleteHandler(w, r)
	} else if r.URL.Path == "/join" && r.Method == "POST" {
		s.JoinClusterHandler(w, r)
	} else if r.URL.Path == "/heartbeat" && r.Method == "POST" {
		s.HeartbeatHandler(w, r)
	} else {
		DPrintf("[%s] received bad request: %s and method %s", s.Id, r.URL.Path, r.Method)
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
	}
}

func (s *Service) HeartbeatHandler(w http.ResponseWriter, r *http.Request) {
	hb := models.Heartbeat{}
	err := json.NewDecoder(r.Body).Decode(&hb)

	if err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	// updating cluster mega somnitelno
	s.LeaderAddr = hb.LeaderAddr
	s.Cluster = hb.Cluster
	lastHeartbeat = time.Now()
	DPrintf("[%s] (is leader %t) received heartbeat from [%s], cluster %s", s.Id, s.IsLeader, hb.Id, s.Cluster)
}

func (s *Service) SendHeartbeat() {
	if !s.IsLeader {
		DPrintf("[%s] tried to send heartbeat but not a leader", s.Id)
		return
	}

	hb := models.Heartbeat{
		Id:         s.Id,
		Addr:       s.Addr,
		LeaderAddr: s.Addr,
		Cluster:    s.Cluster,
	}

	b, err := json.Marshal(hb)
	if err != nil {
		log.Fatalf("cannot marshal heartbeat: %s", err)
	}

	for _, addr := range s.Cluster {
		if addr == s.Addr {
			continue
		}
		r, err := http.NewRequest(http.MethodPost, "http://"+addr+"/heartbeat", bytes.NewReader(b))

		if err != nil {
			DPrintf("[%s] cannot send hearbeat to %s", s.Id, addr)
			return
		}

		c := http.Client{}
		// TODO: add correct error handling and result processing
		res, err := c.Do(r)
		if err != nil {
			DPrintf("[%s] cannot send hearbeat to %s", s.Id, addr)
			continue
		}
		res.Body.Close()
		DPrintf("[%s] (is leader %t) broadcasted heartbeat to %s\n cluster: %v", s.Id, s.IsLeader, addr, s.Cluster)
	}
}

func (s *Service) CheckLeader() {
	if time.Since(lastHeartbeat) > leaderTimeout {
		DPrintf("[%s] time since last heartbeat expired (since %v, leader timeout %v", s.Id, time.Since(lastHeartbeat), leaderTimeout)
		s.StartElection()
	}
}

func (s *Service) StartElection() {
	s.IsLeader = true
	s.LeaderAddr = s.Addr
	DPrintf("[%s] is leader now and sent his first heartbeat", s.Id)
	s.SendHeartbeat()
}

func (s *Service) JoinClusterHandler(w http.ResponseWriter, r *http.Request) {

	if !s.IsLeader {
		DPrintf("[%s] received join request but not a leader, redirecting to %s", s.Id, s.LeaderAddr)
		body, _ := json.Marshal(map[string]string{"leader": s.LeaderAddr})
		w.WriteHeader(http.StatusSeeOther)
		w.Write(body)
		return
	}
	DPrintf("[%s] handling join request", s.Id)
	hb := models.Heartbeat{}
	err := json.NewDecoder(r.Body).Decode(&hb)

	if err != nil {
		DPrintf("[%s] cannot unmarshol join request body")
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}

	if hb.ReplFactor != s.ReplFact {
		message := fmt.Sprintf("replication factror missmatch got: %d, expected: %d", hb.ReplFactor, s.ReplFact)
		http.Error(w, message, http.StatusBadRequest)
		return
	}

	s.Cluster = append(s.Cluster, hb.Addr)

	hb = models.Heartbeat{
		Id:         s.Id,
		Addr:       s.Addr,
		ReplFactor: s.ReplFact,
		Cluster:    s.Cluster,
	}
	b, err := json.Marshal(hb)
	if err != nil {
		DPrintf("[%s] cannot marshal join response body")
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write(b)
}

func (s *Service) JoinCluster(nodeAddr string) error {
	hb := models.Heartbeat{
		Id:         s.Id,
		Addr:       s.Addr,
		ReplFactor: s.ReplFact,
		Cluster:    s.Cluster,
	}

	bodyMarshalled, _ := json.Marshal(hb)
	req, err := http.NewRequest(http.MethodPost, "http://"+nodeAddr+"/join", bytes.NewReader(bodyMarshalled))
	req.Header.Set("Content-Type", "application/json")
	if err != nil {
		return err
	}
	client := http.Client{}
	res, err := client.Do(req)

	lastHeartbeat = time.Now()

	if err != nil {
		return err
	}
	defer res.Body.Close()
	DPrintf("status code in join cluster: %d", res.StatusCode)
	if res.StatusCode == http.StatusSeeOther {
		lAddr := map[string]string{}
		body, _ := io.ReadAll(res.Body)
		json.Unmarshal(body, &lAddr)
		fmt.Println(lAddr["leader"])
		if err := s.JoinCluster(lAddr["leader"]); err != nil {
			return err
		}
		return nil
	} else if res.StatusCode > 201 {
		b, _ := io.ReadAll(res.Body)

		return errors.New(res.Status + " " + string(b))
	}

	err = json.NewDecoder(res.Body).Decode(&hb)
	if err != nil {
		return err
	}

	s.Cluster = hb.Cluster

	DPrintf("[%s] joined cluster successfully", s.Id)
	return nil
}

func isUUID(id string) bool {
	_, err := uuid.Parse(id)
	if err != nil {
		return false
	}
	return true
}

func (s *Service) GetHandler(w http.ResponseWriter, r *http.Request) {
	id := r.Header.Get("uuid")

	if id == "" {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}

	if ok := isUUID(id); !ok {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}

	v, ok := s.storage.Get(id)

	if !ok {
		http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
		return
	}

	b, err := json.Marshal(v)

	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	// TODO: add proper error handling of write(b)
	w.WriteHeader(http.StatusOK)
	w.Write(b)

	DPrintf("[%s] successfully perfrormed GET op", s.Id)
}

func (s *Service) SetHandler(w http.ResponseWriter, r *http.Request) {
	data := models.Data{}
	data.Uuid = r.Header.Get("uuid")
	err := json.NewDecoder(r.Body).Decode(&data)

	if err != nil {
		DPrintf("cannot unmarshal body from set/put request: %s", err)
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}

	if ok := isUUID(data.Uuid); !ok {

		DPrintf("[%s] not proper uuid: %s", s.Id, data.Uuid)
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)

		return
	}

	valueInBytes, _ := json.Marshal(data.Value)

	s.storage.Put(data.Uuid, string(valueInBytes))

	defer func() {
		if err == nil {
			DPrintf("[%s] successfully perfrormed SET op", s.Id)
		}
	}()

	if r.Header.Get("Replica") == "true" {
		return
	}

	operation := Op{Op: "SET"}
	operation.Data = data

	var numOfReplicas int

	if numOfReplicas, err = s.replicateOperation(&operation); err != nil {
		DPrintf("[%s] cannot replicate opeartion SET with error: %s", s.Id, err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)

	responseString := fmt.Sprintf("Created (%d replicas)", numOfReplicas)
	body, err := json.Marshal(map[string]string{"result": responseString})

	_, err = w.Write(body)

	if err != nil {
		DPrintf("[%s] error while writing response for SET op: %s", s.Id, err)
		return
	}

}

func (s *Service) DeleteHandler(w http.ResponseWriter, r *http.Request) {
	id := w.Header().Get("uuid")

	if id == "" {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}

	operation := Op{Op: "DELETE"}

	s.storage.Delete(id)

	var err error
	defer func() {
		if err == nil {
			DPrintf("[%s] successfully perfrormed DELETE op", s.Id)
		}
	}()

	if r.Header.Get("Replica") == "true" {
		return
	}

	var numOfReplicas int

	if numOfReplicas, err = s.replicateOperation(&operation); err != nil {
		DPrintf("[%s] cannot replicate opeartion SET with error: %s", s.Id, err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	responseString := fmt.Sprintf("Deleted (%d replicas)", numOfReplicas)
	body, err := json.Marshal(map[string]string{"result": responseString})
	_, err = w.Write(body)

	if err != nil {
		DPrintf("[%s] error while writing response for SET op: %s", s.Id, err)
		return
	}
}

func (s *Service) replicateOperation(op *Op) (int, error) {
	i := 0
	addr := ""
	c := http.Client{}

	for i, addr = range s.Cluster {

		if addr == s.Addr {
			continue
		}

		var req *http.Request

		if op.Op == "SET" {
			b, _ := json.Marshal(op.Data)
			req, _ = http.NewRequest(http.MethodPost, "http://"+addr+"/set", bytes.NewReader(b))
		} else if op.Op == "DELETE" {
			req, _ = http.NewRequest(http.MethodDelete, "http://"+addr+"/delete", nil)
			req.Header.Set("uuid", op.Data.Uuid)
		}

		req.Header.Set("Replica", "true")
		res, err := c.Do(req)
		if err != nil {
			return i, err
		}

		res.Body.Close()

		if res.StatusCode > 201 {
			b, _ := io.ReadAll(res.Body)
			return i, errors.New(res.Status + " " + string(b))
		}
		DPrintf("[%s] sent replication of op [%s] to [%s]", s.Id, op.Op, addr)
	}

	return i, nil
}
