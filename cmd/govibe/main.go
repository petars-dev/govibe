package main

import (
    "github.com/sirupsen/logrus"
)

func main() {
    if err := Execute(); err != nil {
        logrus.Fatal(err)
    }
}
