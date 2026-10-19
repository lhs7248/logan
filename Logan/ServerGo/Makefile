.PHONY: test race vet build run java-baseline

test:
	go test -tags stdjson ./...
race:
	go test -tags stdjson -race ./...
vet:
	go vet -tags stdjson ./...
build:
	./build.sh
run:
	go run -tags stdjson .
java-baseline:
	mvn -B -f tests/legacy-java/pom.xml test
