FROM public.ecr.aws/docker/library/node:22-alpine AS web
WORKDIR /app
RUN npm install -g pnpm@10.34.9
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml .npmrc ./
RUN pnpm install --frozen-lockfile
COPY index.html vite.config.ts tsconfig.json tsconfig.tests.json ./
COPY src ./src
COPY public ./public
COPY scripts ./scripts
COPY tests ./tests
RUN pnpm run build

FROM public.ecr.aws/docker/library/golang:1.23-alpine AS build
WORKDIR /src
COPY server/ ./
COPY --from=web /app/dist ./web/dist
RUN CGO_ENABLED=0 go test ./... \
    && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/cilihub .

FROM public.ecr.aws/docker/library/alpine:3.20
RUN sed -i s,dl-cdn.alpinelinux.org,mirrors.aliyun.com,g /etc/apk/repositories \
    && apk add --no-cache ca-certificates tzdata \
    && adduser -D -u 10001 cilihub \
    && mkdir -p /data \
    && chown cilihub:cilihub /data
COPY --from=build /out/cilihub /usr/local/bin/cilihub
COPY LICENSE /usr/share/licenses/cilihub/LICENSE
USER 10001:10001
ENV CILIHUB_DATA_DIR=/data CILIHUB_LISTEN=:8080
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/cilihub"]
