<p align="center">
   <img src="https://github.com/Connorrmcd6/offsidefpl/blob/main/public/assets/offside_banner.svg" alt="OffsideFPL logo" width="750"/>
</p>

## About OffsideFPL

OffsideFPL is a fresh take on Fantasy Premier League (FPL), designed to raise the stakes in your mini leagues! Each week, managers compete to climb the leaderboard while avoiding suspension. You, as a manager, will receive a yellow card if any player in your starting XI commits one of the following actions:

- Misses a penalty
- Scores an own goal
- Receives a red card

You can carry a yellow card indefinitely. However, if you pick up two, you will receive a red card and be suspended for the next game week, resulting in 0 points for that week. You can "clear" a yellow card by submitting a fine approved by your league admin. Make sure to submit your fines before picking up a second yellow card, as submissions will lock once you do! After serving your suspension, your cards reset to zero.

Another objective is winning a game week. If you score the highest points in a week (provided you aren't suspended), you can choose one member to receive a yellow card. If the chosen member already has a yellow card, they will receive a red card (friendships may be tested). Alternatively, you can randomly pick three members to receive a yellow card, but there's a catch—you might pick yourself in the random nomination.

Lastly, each player receives one reverse card per season. This card can be used at any point to reverse a yellow card back to the nominator.

OffsideFPL was created using the Bytesize template repository, which can be found [here](https://github.com/cmcd97/bytesize). If you would like to contribute to OffsideFPL or Bytesize, both are open source!

## Features

- **Preconfigured Pocketbase Backend**: Easily manage your backend with Pocketbase.
- **Modern Frontend Stack**: Utilizes Templ, HTMX, Tailwind CSS, and DaisyUI for a seamless and responsive user interface.
- **Hot Reloading**: Air is preconfigured to enable hot reloading in the browser, enhancing your development workflow.

## Prerequisites

Before you begin, ensure you have the following installed:

1. **Go**: [Download Go](https://go.dev/dl/)
2. **Node and NPM**: [Download Node.js and NPM](https://nodejs.org/en)
3. **Air**: Install Air with the following command:
   ```sh
   curl -sSfL https://raw.githubusercontent.com/air-verse/air/master/install.sh | sh -s -- -b $(go env GOPATH)/bin
   ```
   Then, alias Air (if Air is not found):
   ```sh
   alias air='$(go env GOPATH)/bin/air'
   ```
4. **Templ**: Install Templ with the following command:

   Note: You may need to run the `templ generate --watch --proxy="http://localhost:3000" --open-browser=true` command manually the first time before using the makefile command.

   ```sh
   go install github.com/a-h/templ/cmd/templ@latest
   ```

   Add the Go path to your shell configuration file (`~/.bashrc` or `~/.zshrc`):

   ```sh
   export PATH=$PATH:$(go env GOPATH)/bin
   ```

   Source the file:

   ```sh
   source ~/.bashrc
   # or
   source ~/.zshrc
   ```

   Check the Templ version:

   ```sh
   templ --version
   ```

5. **TailwindCSS**: Install TailwindCSS with NPM:
   ```sh
   npm install -D tailwindcss
   ```
   Initialize TailwindCSS:
   ```sh
   npx tailwindcss init
   ```

## Getting Started

To get started with OffsideFPL, follow these steps:

### Local Development

1. **Clone the Repository**:

   ```sh
   git clone https://github.com/Connorrmcd6/offsidefpl.git
   cd offsidefpl
   ```

2. **Install Dependencies**:

   ```sh
   go mod tidy
   npm install
   ```

3. **Run the Application in Development Mode** (in separate terminals):

   ```sh
   air
   make templ
   make css
   ```

4. **Run the Application in Production Mode**:

   ```sh
   make run
   ```

5. **Build the Binary**:
   After running this command, you will find an `app` binary in the `./bin` directory:
   ```sh
   make build
   ```

### Running with Docker (Recommended for Raspberry Pi or server deployment)

1. **Directory Structure**
   Ensure you have the following structure (for deployment):

   ```
   app/
     bin/
       app        # compiled binary
       .env       # environment file
       pb_data/   # persistent database directory
       public/    # static assets
     Dockerfile
     docker-compose.yml
   ```

2. **Dockerfile**
   Use a Dockerfile that copies your pre-built binary and assets:

   ```dockerfile
   FROM alpine:3.19
   WORKDIR /app
   COPY bin/app .
   COPY bin/public ./public
   COPY bin/.env .
   EXPOSE 8090
   CMD ["./app", "serve", "--http=0.0.0.0:8090"]
   ```

3. **docker-compose.yml**
   Mount the original pb_data directory for persistence:

   ```yaml
   version: "3.8"
   services:
     app:
       build: .
       ports:
         - "8090:8090"
       volumes:
         - ./bin/pb_data:/app/pb_data
       restart: always
   ```

4. **Run the Container**
   From the directory containing your Dockerfile and docker-compose.yml, run:

   ```sh
   docker-compose up -d --build
   ```

   This will start your app in detached mode, with persistent data and automatic restarts.

5. **Access the App**
   Visit `http://<raspberry-pi-ip>:8090` in your browser.

**Note:**

- The `pb_data` directory is mounted as a volume, so your app's data is always persisted on the host.
- Make sure your `.env` file sets `LISTEN_ADDR=0.0.0.0:8090` for external access.
