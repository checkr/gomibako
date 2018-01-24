FROM avcosystems/golang-node

WORKDIR /go/src/github.com/hakobe/gomibako
ADD . .
RUN make
CMD ./gomibako --port=8000
