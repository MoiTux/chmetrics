FROM golang:1.22

# download and cache go dependencies
COPY go.*  ./
RUN go mod download

# copy code
COPY . ./

RUN go build -o chmetrics

ENTRYPOINT ["./chmetrics"]
