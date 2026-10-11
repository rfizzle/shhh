package tools

import (
	"errors"
	"testing"
)

func TestExecuteCommand_RedirectsALongRunningShape(t *testing.T) {
	for _, tc := range []struct{ name, command string }{
		{"npm run dev", "npm run dev"},
		{"npm run start", "npm run start"},
		{"npm run serve with flags", "npm run serve -- --port 3000"},
		{"npm start", "npm start"},
		{"yarn dev", "yarn dev"},
		{"yarn run dev", "yarn run dev"},
		{"pnpm dev", "pnpm dev"},
		{"pnpm run dev", "pnpm run dev"},
		{"bun run dev", "bun run dev"},
		{"vite", "vite"},
		{"vite dev", "npx vite dev --port 5173"},
		{"vite from node_modules", "./node_modules/.bin/vite"},
		{"next dev", "next dev"},
		{"next dev through npx", "npx next dev -p 4000"},
		{"rails s", "rails s"},
		{"rails server", "rails server -p 3001"},
		{"rails s under bundler", "bundle exec rails s"},
		{"flask run", "flask run --port 5000"},
		{"flask run through python", "python3 -m flask run"},
		{"uvicorn", "uvicorn app:app --reload"},
		{"uvicorn through python", "python -m uvicorn app:app"},
		{"http.server", "python -m http.server 8000"},
		{"http.server python3", "python3 -m http.server"},
		{"go run a server package", "go run ./server"},
		{"go run cmd/server", "go run ./cmd/server --addr :8080"},
		{"go run serve", "go run ./cmd/serve"},
		{"go run daemon", "go run ./daemon"},
		{"go run a daemon under cmd", "go run ./cmd/shhhd"},
		{"go run server.go", "go run server.go"},
		{"go run with a build flag", "go run -race ./cmd/server"},
		{"tsc --watch", "tsc --watch"},
		{"tsc -w", "npx tsc -w -p ."},
		{"webpack --watch", "webpack --watch"},
		{"jest --watch", "jest --watch"},
		{"jest --watchAll", "jest --watchAll"},
		{"esbuild --watch", "esbuild src/index.ts --bundle --watch"},
		{"sass --watch", "sass --watch in.scss out.css"},
		{"vite build --watch", "vite build --watch"},
		{"cargo watch", "cargo watch -x test"},
		{"watch", "watch -n 2 kubectl get pods"},
		{"tail -f", "tail -f /var/log/app.log"},
		{"tail -F", "tail -F app.log"},
		{"tail -fn", "tail -fn 50 app.log"},
		{"tail --follow", "tail --follow=name app.log"},
		{"nodemon", "nodemon server.js"},
		{"air", "air"},
		{"docker compose up", "docker compose up"},
		{"docker compose up with a build", "docker compose -f ci.yml up --build web"},
		{"docker-compose up", "docker-compose up"},
		{"after a cd", "cd web && npm run dev"},
		{"after an assignment", "PORT=3000 npm run dev"},
		{"in a pipe", "tail -f app.log | grep ERROR"},
		{"second of a list", "make build; go run ./cmd/server"},
		{"on its own line", "echo ready\nnpm run dev"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !LongRunning(tc.command) {
				t.Errorf("LongRunning(%q) = false", tc.command)
			}
		})
	}
}

func TestExecuteCommand_LeavesTheRestAlone(t *testing.T) {
	for _, command := range []string{
		"go test ./...",
		"go build ./cmd/shhh",
		"go run ./cmd/build",
		"go run ./cmd/add",
		"go run . --help",
		"npm test",
		"npm run build",
		"npm install",
		"yarn install",
		"pnpm build",
		"vite build",
		"next build",
		"rails db:migrate",
		"flask routes",
		"python script.py",
		"python -m pytest -x",
		"python -m json.tool data.json",
		"grep -w main internal/cli/root.go",
		"wc -w README.md",
		"tail -n 20 app.log",
		"tail -20 app.log",
		"git log --oneline | head",
		"cargo build",
		"cargo test",
		"tsc --noEmit",
		"jest --ci",
		"docker compose up -d",
		"docker compose up --detach web",
		"docker compose ps",
		"docker build .",
		"ls -la && cat go.mod",
		"echo 'npm run dev'",
		"echo \"start it: tail -f x && air\"",
		"npm run dev &",
		"nohup npm run dev > dev.log 2>&1 &",
		"timeout 5 npm run dev",
		"curl localhost:3000 2>&1 | head",
		"pip install flask",
		"sleep 3",
		"",
	} {
		t.Run(command, func(t *testing.T) {
			if LongRunning(command) {
				t.Errorf("LongRunning(%q) = true", command)
			}
		})
	}
}

func TestLongRunningError_SaysWhereToGo(t *testing.T) {
	var err error = LongRunningError{}
	var long LongRunningError
	if !errors.As(err, &long) {
		t.Fatal("not found by errors.As")
	}
	want := "this looks long-running: start it with the process tool (action start) and read it with action read"
	if err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}
}
