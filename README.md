# chmetrics: change.org metrics

Is a simple script that get the current number of signatures and goal from change.org.
It then append those values to a Google spreadsheet.

You need Application Default Credentials (ADC) to use the google spreadsheet API,
so you might need to export GOOGLE_APPLICATION_CREDENTIALS with a path to a json key
from a Service account, and don't forget the share the spreadsheet sheet with this "user".
