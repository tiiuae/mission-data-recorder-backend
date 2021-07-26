FROM node:12 AS builder

RUN mkdir /e2e-test
COPY . /e2e-test
WORKDIR /e2e-test
RUN yarn install && yarn run build

FROM node:12
RUN mkdir /e2e-tests
WORKDIR /e2e-test
COPY --from=builder /e2e-test/build ./build
COPY --from=builder /e2e-test/node_modules ./node_modules
COPY ./run-test-kube.sh .

CMD ["/bin/bash"]