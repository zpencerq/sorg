package main

import (
	"bufio"
	"database/sql"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/brandur/sorg"
	"github.com/brandur/sorg/templatehelpers"
	"github.com/joeshaw/envdecode"
	_ "github.com/lib/pq"
	"github.com/russross/blackfriday"
	"github.com/yosssi/ace"
	"github.com/yosssi/gcss"
	"gopkg.in/yaml.v2"
)

var stylesheets = []string{
	"_reset.sass",
	"main.sass",
	"about.sass",
	"fragments.sass",
	"index.sass",
	"photos.sass",
	"quotes.sass",
	"reading.sass",
	"runs.sass",
	"signature.sass",
	"solarized-light.css",
	"tenets.sass",
	"twitter.sass",
}

// Article represents an article to be rendered.
type Article struct {
	// Attributions are any attributions for content that may be included in
	// the article (like an image in the header for example).
	Attributions string `yaml:"attributions"`

	// Content is the HTML content of the article. It isn't included as YAML
	// frontmatter, and is rather split out of an article's Markdown file,
	// rendered, and then added separately.
	Content string `yaml:"-"`

	// HNLink is an optional link to comments on Hacker News.
	HNLink string `yaml:"hn_link"`

	// Hook is a leading sentence or two to succinctly introduce the article.
	Hook string `yaml:"hook"`

	// Image is an optional image that may be included with an article.
	Image string `yaml:"image"`

	// PublishedAt is when the article was published.
	PublishedAt *time.Time `yaml:"published_at"`

	// Title is the article's title.
	Title string `yaml:"title"`

	// TOC is the HTML rendered table of contents of the article. It isn't
	// included as YAML frontmatter, but rather calculated from the article's
	// content, rendered, and then added separately.
	TOC string `yaml:"-"`
}

// Conf contains configuration information for the command.
type Conf struct {
	// BlackSwanDatabaseURL is a connection string for a database to connect to
	// in order to extract books, tweets, runs, etc.
	BlackSwanDatabaseURL string `env:"BLACK_SWAN_DATABASE_URL"`

	// GoogleAnalyticsID is the account identifier for Google Analytics to use.
	GoogleAnalyticsID string `env:"GOOGLE_ANALYTICS_ID"`

	// Verbose is whether the program will print debug output as it's running.
	Verbose bool `env:"VERBOSE,default=false"`
}

// Fragment represents a fragment (that is, a short "stream of consciousness"
// style article) to be rendered.
type Fragment struct {
	// Content is the HTML content of the fragment. It isn't included as YAML
	// frontmatter, and is rather split out of an fragment's Markdown file,
	// rendered, and then added separately.
	Content string `yaml:"-"`

	// Image is an optional image that may be included with a fragment.
	Image string `yaml:"image"`

	// PublishedAt is when the fragment was published.
	PublishedAt *time.Time `yaml:"published_at"`

	// Title is the fragment's title.
	Title string `yaml:"title"`
}

type Run struct {
	Distance float64

	ElevationGain float64

	LocationCity string

	MovingTime time.Duration

	OccurredAt *time.Time
}

var conf Conf

func main() {
	err := envdecode.Decode(&conf)
	if err != nil {
		log.Fatal(err)
	}

	sorg.InitLog(conf.Verbose)

	err = sorg.CreateTargetDirs()
	if err != nil {
		log.Fatal(err)
	}

	err = compileArticles()
	if err != nil {
		log.Fatal(err)
	}

	err = compileFragments()
	if err != nil {
		log.Fatal(err)
	}

	err = compileRuns()
	if err != nil {
		log.Fatal(err)
	}

	err = compileStylesheets()
	if err != nil {
		log.Fatal(err)
	}

	err = linkImageAssets()
	if err != nil {
		log.Fatal(err)
	}
}

func compileArticles() error {
	articleInfos, err := ioutil.ReadDir(sorg.ArticlesDir)
	if err != nil {
		return err
	}

	for _, articleInfo := range articleInfos {
		inPath := sorg.ArticlesDir + articleInfo.Name()
		log.Debugf("Compiling: %v", inPath)

		outName := strings.Replace(articleInfo.Name(), ".md", "", -1)

		raw, err := ioutil.ReadFile(inPath)
		if err != nil {
			return err
		}

		frontmatter, content, err := splitFrontmatter(string(raw))
		if err != nil {
			return err
		}

		var article Article
		err = yaml.Unmarshal([]byte(frontmatter), &article)
		if err != nil {
			return err
		}

		if article.Title == "" {
			return fmt.Errorf("No title for article: %v", inPath)
		}

		if article.PublishedAt == nil {
			return fmt.Errorf("No publish date for article: %v", inPath)
		}

		article.Content = string(renderMarkdown([]byte(content)))

		// TODO: Need a TOC!
		article.TOC = ""

		locals := getLocals(article.Title, map[string]interface{}{
			"Article": article,
		})

		err = renderView(sorg.LayoutsDir+"main", sorg.ViewsDir+"/articles/show",
			sorg.TargetArticlesDir+outName, locals)
		if err != nil {
			return err
		}
	}

	return nil
}

func compileFragments() error {
	fragmentInfos, err := ioutil.ReadDir(sorg.FragmentsDir)
	if err != nil {
		return err
	}

	for _, fragmentInfo := range fragmentInfos {
		inPath := sorg.FragmentsDir + fragmentInfo.Name()
		log.Debugf("Compiling: %v", inPath)

		outName := strings.Replace(fragmentInfo.Name(), ".md", "", -1)

		raw, err := ioutil.ReadFile(inPath)
		if err != nil {
			return err
		}

		frontmatter, content, err := splitFrontmatter(string(raw))
		if err != nil {
			return err
		}

		var fragment Fragment
		err = yaml.Unmarshal([]byte(frontmatter), &fragment)
		if err != nil {
			return err
		}

		if fragment.Title == "" {
			return fmt.Errorf("No title for fragment: %v", inPath)
		}

		if fragment.PublishedAt == nil {
			return fmt.Errorf("No publish date for fragment: %v", inPath)
		}

		fragment.Content = string(renderMarkdown([]byte(content)))

		locals := getLocals(fragment.Title, map[string]interface{}{
			"Fragment": fragment,
		})

		err = renderView(sorg.LayoutsDir+"main", sorg.ViewsDir+"/fragments/show",
			sorg.TargetFragmentsDir+outName, locals)
		if err != nil {
			return err
		}
	}

	return nil
}

// Gets a map of local values for use while rendering a template and includes
// a few "special" values that are globally relevant to all templates.
func getLocals(title string, locals map[string]interface{}) map[string]interface{} {
	defaults := map[string]interface{}{
		"BodyClass":         "",
		"GoogleAnalyticsID": conf.GoogleAnalyticsID,
		"Release":           sorg.Release,
		"Title":             title,
		"ViewportWidth":     "device-width",
	}

	for k, v := range locals {
		defaults[k] = v
	}

	return defaults
}

func compileRuns() error {
	var runs []*Run

	// Give all these arrays 0 elements (instead of null) in case no Black Swan
	// data gets loaded but we still need to render the page.
	lastYearXDays := []time.Time{}
	lastYearYDistances := []float64{}

	byYearXYears := []string{}
	byYearYDistances := []float64{}

	if conf.BlackSwanDatabaseURL != "" {
		db, err := sql.Open("postgres", conf.BlackSwanDatabaseURL)
		if err != nil {
			log.Fatal(err)
		}

		rows, err := db.Query(`
			SELECT
				(metadata -> 'distance')::float,
				(metadata -> 'total_elevation_gain')::float,
				(metadata -> 'location_city'),
				-- we multiply by 10e9 here because a Golang time.Duration is
				-- an int64 represented in nanoseconds
				(metadata -> 'moving_time')::bigint * 1000000000,
				(metadata -> 'occurred_at_local')::timestamptz
			FROM events
			WHERE type = 'strava'
				AND metadata -> 'type' = 'Run'
			ORDER BY occurred_at DESC
			LIMIT 30
		`)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var locationCity *string
			var run Run

			err = rows.Scan(
				&run.Distance,
				&run.ElevationGain,
				&locationCity,
				&run.MovingTime,
				&run.OccurredAt,
			)
			if err != nil {
				return err
			}

			if locationCity != nil {
				run.LocationCity = *locationCity
			}

			runs = append(runs, &run)
		}
		err = rows.Err()
		if err != nil {
			return err
		}

		//
		// runs over the last year
		//

		rows, err = db.Query(`
			WITH runs AS (
				SELECT *,
					(metadata -> 'occurred_at_local')::timestamptz AS occurred_at_local,
					-- convert to distance in kilometers
					((metadata -> 'distance')::float / 1000.0) AS distance
				FROM events
				WHERE type = 'strava'
					AND metadata -> 'type' = 'Run'
			),

			runs_days AS (
				SELECT date_trunc('day', occurred_at_local) AS day,
					SUM(distance) AS distance
				FROM runs
				WHERE occurred_at_local > NOW() - '180 days'::interval
					GROUP BY day
			),

			-- generates a baseline series of every day in the last 180 days
			-- along with a zeroed distance which we will then add against the
			-- actual runs we extracted
			days AS (
				SELECT i::date AS day,
					0::float AS distance
				FROM generate_series(NOW() - '180 days'::interval,
					NOW(), '1 day'::interval) i
			)

			SELECT d.day,
				d.distance + COALESCE(rd.distance, 0::float)
			FROM days d
				LEFT JOIN runs_days rd ON d.day = rd.day
			ORDER BY day ASC
		`)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var day time.Time
			var distance float64

			err = rows.Scan(
				&day,
				&distance,
			)
			if err != nil {
				return err
			}

			lastYearXDays = append(lastYearXDays, day)
			lastYearYDistances = append(lastYearYDistances, distance)
		}
		err = rows.Err()
		if err != nil {
			return err
		}

		//
		// run distance per year
		//

		rows, err = db.Query(`
			WITH runs AS (
				SELECT *,
					(metadata -> 'occurred_at_local')::timestamptz AS occurred_at_local,
					-- convert to distance in kilometers
					((metadata -> 'distance')::float / 1000.0) AS distance
				FROM events
				WHERE type = 'strava'
					AND metadata -> 'type' = 'Run'
			)

			SELECT date_part('year', occurred_at_local)::text AS year,
				SUM(distance)
			FROM runs
			GROUP BY year
			ORDER BY year DESC
		`)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var distance float64
			var year string

			err = rows.Scan(
				&year,
				&distance,
			)
			if err != nil {
				return err
			}

			byYearXYears = append(byYearXYears, year)
			byYearYDistances = append(byYearYDistances, distance)
		}
		err = rows.Err()
		if err != nil {
			return err
		}
	}

	locals := getLocals("Runs", map[string]interface{}{
		"Runs": runs,

		// chart: runs over last year
		"LastYearXDays":      lastYearXDays,
		"LastYearYDistances": lastYearYDistances,

		// chart: run distance by year
		"ByYearXYears":     byYearXYears,
		"ByYearYDistances": byYearYDistances,
	})

	err := renderView(sorg.LayoutsDir+"main", sorg.ViewsDir+"/runs/index",
		sorg.TargetDir+"runs", locals)
	if err != nil {
		return err
	}

	return nil
}

func compileStylesheets() error {
	outFile, err := os.Create(sorg.TargetVersionedAssetsDir + "app.css")
	if err != nil {
		return err
	}
	defer outFile.Close()

	for _, stylesheet := range stylesheets {
		inPath := sorg.StylesheetsDir + stylesheet
		log.Debugf("Compiling: %v", inPath)

		inFile, err := os.Open(inPath)
		if err != nil {
			return err
		}

		outFile.WriteString("/* " + stylesheet + " */\n\n")

		if strings.HasSuffix(stylesheet, ".sass") {
			_, err := gcss.Compile(outFile, inFile)
			if err != nil {
				return fmt.Errorf("Error compiling %v: %v", inPath, err)
			}
		} else {
			_, err := io.Copy(outFile, inFile)
			if err != nil {
				return err
			}
		}

		outFile.WriteString("\n\n")
	}

	return nil
}

func linkImageAssets() error {
	assets, err := ioutil.ReadDir(sorg.ImagesDir)
	if err != nil {
		return err
	}

	for _, asset := range assets {
		log.Debugf("Linking image asset: %v", asset)

		// we use absolute paths for source and destination because not doing
		// so can result in some weird symbolic link inception
		source, err := filepath.Abs(sorg.ImagesDir + asset.Name())
		if err != nil {
			return err
		}

		dest, err := filepath.Abs(sorg.TargetAssetsDir + asset.Name())
		if err != nil {
			return err
		}

		err = os.RemoveAll(dest)
		if err != nil {
			return err
		}

		err = os.Symlink(source, dest)
		if err != nil {
			return err
		}
	}

	return nil
}

func renderMarkdown(source []byte) []byte {
	htmlFlags := 0
	htmlFlags |= blackfriday.HTML_SMARTYPANTS_DASHES
	htmlFlags |= blackfriday.HTML_SMARTYPANTS_FRACTIONS
	htmlFlags |= blackfriday.HTML_SMARTYPANTS_LATEX_DASHES
	htmlFlags |= blackfriday.HTML_USE_SMARTYPANTS
	htmlFlags |= blackfriday.HTML_USE_XHTML

	extensions := 0
	extensions |= blackfriday.EXTENSION_AUTO_HEADER_IDS
	extensions |= blackfriday.EXTENSION_AUTOLINK
	extensions |= blackfriday.EXTENSION_FENCED_CODE
	extensions |= blackfriday.EXTENSION_HEADER_IDS
	extensions |= blackfriday.EXTENSION_LAX_HTML_BLOCKS
	extensions |= blackfriday.EXTENSION_NO_INTRA_EMPHASIS
	extensions |= blackfriday.EXTENSION_TABLES
	extensions |= blackfriday.EXTENSION_SPACE_HEADERS
	extensions |= blackfriday.EXTENSION_STRIKETHROUGH

	renderer := blackfriday.HtmlRenderer(htmlFlags, "", "")
	return blackfriday.Markdown(source, renderer, extensions)
}

func renderView(layout, view, target string, locals map[string]interface{}) error {
	log.Debugf("Rendering: %v", target)

	template, err := ace.Load(layout, view, &ace.Options{FuncMap: templatehelpers.FuncMap})
	if err != nil {
		return err
	}

	file, err := os.Create(target)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := bufio.NewWriter(file)
	defer writer.Flush()

	err = template.Execute(writer, locals)
	if err != nil {
		return err
	}

	return nil
}

var errBadFrontmatter = fmt.Errorf("Unable to split YAML frontmatter")

func splitFrontmatter(content string) (string, string, error) {
	parts := regexp.MustCompile("(?m)^---").Split(content, 3)

	if len(parts) > 1 && parts[0] != "" {
		return "", "", errBadFrontmatter
	} else if len(parts) == 2 {
		return "", strings.TrimSpace(parts[1]), nil
	} else if len(parts) == 3 {
		return strings.TrimSpace(parts[1]), strings.TrimSpace(parts[2]), nil
	}

	return "", strings.TrimSpace(parts[0]), nil
}