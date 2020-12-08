package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api"
	"github.com/ahmdrz/goinsta/v2"
	"gopkg.in/yaml.v3"
)

type TG struct {
	Token  string `yaml:"token"`
	ChatID int64  `yaml:"chatID"`
}

type IG struct {
	Username string `yaml:"username,omitempty"`
	Password string `yaml:"password,omitempty"`
}

type ConfigData struct {
	TG               `yaml:"TG"`
	IG               `yaml:"IG,omitempty"`
	PollingPeriod    time.Duration `yaml:"pollingPeriod"`
	CrosspostNewOnly bool          `yaml:"crosspostNewOnly"`
}

type Story struct {
	ID    string
	URL   string
	new   bool
	video bool
}

var knownStories map[string]bool

func syncWithKnownSet(storyData []Story) {
	if knownStories == nil {
		knownStories = make(map[string]bool)
	}
	for idx, _ := range storyData {
		if _, ok := knownStories[storyData[idx].ID]; ok {
			//log.Printf("Story '%s' is known", storyData[idx].ID)
		} else {
			knownStories[storyData[idx].ID] = true
			storyData[idx].new = true
			//log.Printf("Story '%s' is new", storyData[idx].ID)
		}
	}
}

func removeFromKnownSet(storyID string) {
	knownStories[storyID] = false
}

func doIGAuth(login, pass string) (instaHandle *goinsta.Instagram, err error) {
	authFile := ".igauthdata"

	instaHandle, err = goinsta.Import(authFile)
	if err == nil {
		log.Println("Reusing previous IG auth data from file", authFile)
	} else {
		err = nil
		log.Println("Logging in using name and pass")
		instaHandle = goinsta.New(login, pass)
	}

	if err = instaHandle.Login(); err != nil {
		// ! auth can fail with status code 400 when auth file is used, but handle still works after
		err = fmt.Errorf("login failed: %v", err)
		return
	}
	log.Println("Logged in successfully")

	if err = instaHandle.Export(authFile); err != nil {
		log.Println("failed to store auth data, will need to use login and pass next time:", err)
	}
	return
}

func storyData(gram *goinsta.Instagram) (stories []Story, err error) {
	//log.Println("Fetching IG story data...")
	//defer log.Println("Done fetching IG story data")
	myID := gram.Account.ID

	tray, err := gram.Timeline.Stories()
	if err != nil {
		err = fmt.Errorf("failed to load story data: %v", err)
		return
	}

	for _, storySet := range tray.Stories {
		if storySet.User.ID == myID {
			for _, story := range storySet.Items {
				urlToLoad := ""
				//log.Println("Saving candidate data. Taken:", story.TakenAt, "ID:", story.ID)

				isVid := false
				if len(story.Videos) != 0 {
					urlToLoad = story.Videos[0].URL
					isVid = true
				} else if len(story.Images.Versions) != 0 {
					urlToLoad = story.Images.GetBest()
				}

				if urlToLoad != "" {
					stories = append(stories, Story{ID: story.ID, URL: urlToLoad, video: isVid})
				}
			}
			break
		}
	}
	return
}

func shareToTG(handle *tgbotapi.BotAPI, chatID int64, stories []Story) {
	for idx, _ := range stories {
		if !stories[idx].new {
			continue
		}

		var content tgbotapi.Chattable
		if stories[idx].video {
			content = tgbotapi.NewVideoShare(chatID, stories[idx].URL)
		} else {
			content = tgbotapi.NewPhotoShare(chatID, stories[idx].URL)
		}
		_, err := handle.Send(content)
		if err != nil {
			removeFromKnownSet(stories[idx].ID)
			log.Printf("Failed to post story '%v' through TG message: %v", stories[idx].ID, err)
		}
		log.Printf("Story '%v' crossposted to TG", stories[idx].ID)
	}
	return
}

func main() {
	cfgFilePath := flag.String("cfg", "config.yml", "path to config file")
	flag.Parse()
	cfgBytes, err := ioutil.ReadFile(*cfgFilePath)
	if err != nil {
		log.Printf("Config file '%v' read error: %v", *cfgFilePath, err)
		return
	}

	var cfg ConfigData
	err = yaml.Unmarshal(cfgBytes, &cfg)
	if err != nil {
		log.Printf("Config data parsing error: %v", err)
		return
	}

	igHandle, err := doIGAuth(cfg.IG.Username, cfg.IG.Password)
	if err != nil {
		// read code of doIGAuth, failure is possible
		log.Println("IG authentication (allowed to fail, see sources):", err)
	}

	tgHandle, err := goTGAuth(cfg.TG.Token)

	if err != nil {
		log.Println("TG authentication:", err, tgHandle)
		return
	}

	// do first iteration right away, others will follow ticker
	doCrosspost(igHandle, tgHandle, cfg.TG.ChatID, cfg.CrosspostNewOnly)
	ticker := time.NewTicker(cfg.PollingPeriod)
	for {
		select {
		case <-ticker.C:
			doCrosspost(igHandle, tgHandle, cfg.TG.ChatID, false)
		}
	}

	fmt.Println("Done")
}

func doCrosspost(igHandle *goinsta.Instagram, tgHandle *tgbotapi.BotAPI, tgChatID int64, firstIteration bool) {
	stories, err := storyData(igHandle)
	if err != nil {
		log.Println("Failed to load IG story data:", err)
	}

	syncWithKnownSet(stories)
	// only register stories appeared before first iteration, do not load and republish to TG

	if firstIteration {
		log.Printf("Skipping %d stories as they were published before launch of the app", len(stories))
		firstIteration = false
		return
	}

	shareToTG(tgHandle, tgChatID, stories)
}

func goTGAuth(token string) (bot *tgbotapi.BotAPI, err error) {
	bot, err = tgbotapi.NewBotAPI(token)
	return
}
