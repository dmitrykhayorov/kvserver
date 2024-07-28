package service

import "log"

var Debug = true

func DPrintf(f string, v ...interface{}) {
	if Debug {
		log.Printf(f, v...)
	}
}
